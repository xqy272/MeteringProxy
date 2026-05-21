package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"ai-gateway-metering-proxy/internal/cliproxy"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/store"
)

type ProviderAdapter interface {
	BuildRequest(provider string) map[string]interface{}
	ParseResponse(p *Poller, provider string, body []byte, nowStr string, nowUnix int64) (quotaParseResult, error)
}

type quotaParseResult struct {
	Count            int
	CredentialHashes []string
}

type genericAdapter struct{}

type Poller struct {
	client       *cliproxy.Client
	database     store.QuotaStore
	hasher       *hash.Hasher
	cfg          config.QuotaConfig
	mu           sync.RWMutex
	cache        []db.QuotaCurrentRow
	lastAt       time.Time
	apiCallAvail bool
	stopCh       chan struct{}
	doneCh       chan struct{}
	refreshCh    chan struct{}
}

func NewPoller(client *cliproxy.Client, database store.QuotaStore, hasher *hash.Hasher, cfg config.QuotaConfig) *Poller {
	return &Poller{
		client:    client,
		database:  database,
		hasher:    hasher,
		cfg:       cfg,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		refreshCh: make(chan struct{}, 1),
	}
}

func (p *Poller) Start() {
	go p.probeAndLoop()
}

func (p *Poller) Stop() {
	close(p.stopCh)
	<-p.doneCh
}

func (p *Poller) probeAndLoop() {
	defer close(p.doneCh)
	p.probeAPICall()
	p.poll()
	cleanupTicker := time.NewTicker(p.cfg.DiagnosticRetention)
	ticker := time.NewTicker(p.cfg.CacheTTL)
	defer ticker.Stop()
	defer cleanupTicker.Stop()
	probeTicker := time.NewTicker(p.cfg.CacheTTL * 2)
	defer probeTicker.Stop()
	for {
		select {
		case <-ticker.C:
			p.poll()
		case <-p.refreshCh:
			p.probeAPICall()
			p.poll()
		case <-probeTicker.C:
			p.probeAPICall()
		case <-cleanupTicker.C:
			p.cleanup()
		case <-p.stopCh:
			return
		}
	}
}

func (p *Poller) cleanup() {
	cutoff := time.Now().Add(-p.cfg.DiagnosticRetention)
	if err := p.database.DeleteStaleQuotaRefreshEvents(cutoff); err != nil {
		log.Printf("quota cleanup error: %v", err)
	}
}

func (p *Poller) probeAPICall() {
	if p.client == nil {
		p.mu.Lock()
		p.apiCallAvail = false
		p.mu.Unlock()
		return
	}
	body := bytes.NewReader([]byte(`{"provider":"__metering_probe__","dry_run":true}`))
	_, statusCode, err := p.client.DoAPICall("POST", "/api-call", body)
	if err != nil {
		p.mu.Lock()
		p.apiCallAvail = false
		p.mu.Unlock()
		return
	}
	p.mu.Lock()
	p.apiCallAvail = statusCode >= 200 && statusCode < 300
	p.mu.Unlock()
}

func (p *Poller) APICallAvailable() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.apiCallAvail
}

func (p *Poller) poll() {
	if !p.cfg.Enabled {
		return
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	nowUnix := now.Unix()
	if !p.APICallAvailable() {
		for _, provider := range p.cfg.Providers {
			p.recordRefreshEvent(provider, "", "probe", "error", "api_call_unavailable", 0, nowStr, nowUnix)
			p.markProviderStale(provider, "api_call_unavailable", nowStr, nowUnix)
		}
		p.refreshDBState()
		return
	}

	concurrency := p.cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, provider := range p.cfg.Providers {
		provider := provider
		select {
		case <-p.stopCh:
			return
		default:
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p.pollProvider(provider, nowStr, nowUnix)
		}()
	}
	wg.Wait()

	p.refreshDBState()
}

func (p *Poller) pollProvider(provider, nowStr string, nowUnix int64) {
	adapter, ok := adapterForProvider(provider)
	if !ok {
		p.recordUnsupportedProvider(provider, nowStr, nowUnix)
		return
	}
	path := "/api-call"
	body := adapter.BuildRequest(provider)
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		log.Printf("quota poll: marshal error for %s: %v", provider, err)
		return
	}
	startTime := time.Now()
	respBody, statusCode, err := p.client.DoAPICall("POST", path, bytes.NewReader(bodyBytes))
	durationMs := time.Since(startTime).Milliseconds()
	if err != nil {
		log.Printf("quota poll: error for %s: %v", provider, err)
		p.recordRefreshEvent(provider, "", "api_call", "error", "api_call_unavailable", durationMs, nowStr, nowUnix)
		p.markProviderStale(provider, "api_call_unavailable", nowStr, nowUnix)
		return
	}
	if statusCode != 200 {
		p.recordRefreshEvent(provider, "", "api_call", "error", "provider_error", durationMs, nowStr, nowUnix)
		p.markProviderStale(provider, "provider_error", nowStr, nowUnix)
		return
	}
	result, err := adapter.ParseResponse(p, provider, respBody, nowStr, nowUnix)
	if err != nil {
		log.Printf("quota poll: parse error for %s: %v", provider, err)
		p.recordRefreshEvent(provider, "", "parse", "error", "parse_error", durationMs, nowStr, nowUnix)
		p.markProviderStale(provider, "parse_error", nowStr, nowUnix)
		return
	}
	if result.Count == 0 {
		p.recordRefreshEvent(provider, "", "parse", "error", "parse_error", durationMs, nowStr, nowUnix)
		p.markProviderStale(provider, "parse_error", nowStr, nowUnix)
		return
	}
	for _, credHash := range result.CredentialHashes {
		p.recordRefreshEvent(provider, credHash, "api_call", "success", "available", durationMs, nowStr, nowUnix)
	}
	p.recordRefreshEvent(provider, "", "api_call", "success", "available", durationMs, nowStr, nowUnix)
}

func (p *Poller) markProviderStale(provider, adapterStatus, nowStr string, nowUnix int64) {
	rows, err := p.database.AllQuotaCurrent()
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.Provider == provider && row.Status != "stale" && row.Status != "error" {
			row.Status = "stale"
			row.AdapterStatus = adapterStatus
			row.CheckedAt = nowStr
			row.CheckedAtUnix = nowUnix
			if err := p.database.UpsertQuotaCurrent(&row); err != nil {
				log.Printf("quota stale mark error for %s/%s: %v", provider, row.CredentialHash, err)
			}
		}
	}
}

func (genericAdapter) BuildRequest(provider string) map[string]interface{} {
	return map[string]interface{}{
		"provider": provider,
	}
}

func (genericAdapter) ParseResponse(p *Poller, provider string, body []byte, nowStr string, nowUnix int64) (quotaParseResult, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return quotaParseResult{}, err
	}
	data, ok := raw["data"].([]interface{})
	if !ok {
		single, ok := raw["data"].(map[string]interface{})
		if ok {
			data = []interface{}{single}
		} else {
			return quotaParseResult{}, fmt.Errorf("missing quota data")
		}
	}
	result := quotaParseResult{}
	for _, item := range data {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		credHash, err := p.processQuotaEntry(provider, entry, nowStr, nowUnix)
		if err != nil {
			return result, err
		}
		result.Count++
		if credHash != "" {
			result.CredentialHashes = append(result.CredentialHashes, credHash)
		}
	}
	return result, nil
}

func (p *Poller) processQuotaEntry(provider string, entry map[string]interface{}, nowStr string, nowUnix int64) (string, error) {
	credHash := ""
	if key, ok := entry["key"].(string); ok && key != "" {
		credHash = p.hashQuotaCredential(provider, "key:"+key)
	} else if h, ok := entry["credential_hash"].(string); ok && h != "" {
		credHash = p.hashQuotaCredential(provider, "credential_hash:"+h)
	} else if idx, ok := entry["auth_index"]; ok {
		credHash = p.hashQuotaCredential(provider, "auth_index:"+fmt.Sprint(idx))
	} else if source, ok := entry["source"].(string); ok && source != "" {
		credHash = p.hashQuotaCredential(provider, "source:"+source)
	}
	windowKey := ""
	if wk, ok := entry["window_key"].(string); ok {
		windowKey = wk
	}
	if windowKey == "" {
		windowKey = "default"
	}
	if credHash == "" {
		return "", fmt.Errorf("missing credential identity")
	}
	row := db.QuotaCurrentRow{
		Provider:        provider,
		CredentialHash:  credHash,
		WindowKey:       windowKey,
		CheckedAt:       nowStr,
		CheckedAtUnix:   nowUnix,
		Plan:            jsonString(entry, "plan"),
		LimitAmount:     jsonFloat(entry, "limit_amount"),
		RemainingAmount: jsonFloat(entry, "remaining_amount"),
		UsedAmount:      jsonFloat(entry, "used_amount"),
		Unit:            jsonString(entry, "unit"),
		Status:          jsonQuotaStatus(jsonFloat(entry, "remaining_amount"), jsonFloat(entry, "limit_amount"), p.cfg.LowThreshold, p.cfg.WarningThreshold),
		QuotaSupported:  1,
		AdapterStatus:   "available",
	}
	if expAt, ok := entry["expires_at"].(string); ok {
		row.ExpiresAt = expAt
		if t, err := time.Parse(time.RFC3339, expAt); err == nil {
			row.ExpiresAtUnix = t.Unix()
		}
	}
	if resetAt, ok := entry["reset_at"].(string); ok {
		row.ResetAt = resetAt
		if t, err := time.Parse(time.RFC3339, resetAt); err == nil {
			row.ResetAtUnix = t.Unix()
		}
	}
	if err := p.database.UpsertQuotaCurrent(&row); err != nil {
		log.Printf("quota upsert error for %s/%s: %v", provider, credHash, err)
		return "", err
	}
	return credHash, nil
}

func (p *Poller) recordUnsupportedProvider(provider, nowStr string, nowUnix int64) {
	credHash := p.hashQuotaCredential(provider, "unsupported")
	row := &db.QuotaCurrentRow{
		Provider:       provider,
		CredentialHash: credHash,
		WindowKey:      "default",
		CheckedAt:      nowStr,
		CheckedAtUnix:  nowUnix,
		Status:         "unsupported",
		QuotaSupported: 0,
		AdapterStatus:  "unsupported",
		ErrorClass:     "quota_unsupported",
		Partial:        1,
	}
	if err := p.database.UpsertQuotaCurrent(row); err != nil {
		log.Printf("quota unsupported upsert error for %s: %v", provider, err)
	}
	p.recordRefreshEvent(provider, credHash, "adapter", "error", "unsupported", 0, nowStr, nowUnix)
}

func adapterForProvider(provider string) (ProviderAdapter, bool) {
	switch provider {
	case "claude", "codex", "kimi":
		return genericAdapter{}, true
	default:
		return nil, false
	}
}

func (p *Poller) hashQuotaCredential(provider, material string) string {
	if p.hasher == nil {
		return ""
	}
	return p.hasher.Hash("quota_credential:" + provider + ":" + material)
}

func jsonQuotaStatus(remaining, limit, lowThreshold, warningThreshold float64) string {
	if limit <= 0 {
		return "unknown"
	}
	ratio := remaining / limit
	if ratio <= 0 {
		return "exhausted"
	}
	if ratio < lowThreshold {
		return "low"
	}
	if ratio < warningThreshold {
		return "warning"
	}
	return "ok"
}

func (p *Poller) recordRefreshEvent(provider, credHash, phase, status, adapterStatus string, durationMs int64, nowStr string, nowUnix int64) {
	errorClass := ""
	if status == "error" {
		errorClass = adapterStatus
		if errorClass == "" {
			errorClass = "quota_refresh_failed"
		}
	}
	row := &db.QuotaRefreshEventRow{
		CheckedAt:      nowStr,
		CheckedAtUnix:  nowUnix,
		Provider:       provider,
		CredentialHash: credHash,
		Phase:          phase,
		Status:         status,
		AdapterStatus:  adapterStatus,
		DurationMs:     durationMs,
		ErrorClass:     errorClass,
	}
	if err := p.database.InsertQuotaRefreshEvent(row); err != nil {
		log.Printf("quota refresh event insert error: %v", err)
	}
}

func (p *Poller) refreshDBState() {
	rows, err := p.database.AllQuotaCurrent()
	if err != nil {
		return
	}
	p.mu.Lock()
	p.cache = rows
	p.lastAt = time.Now()
	p.mu.Unlock()
}

func (p *Poller) Snapshot() ([]db.QuotaCurrentRow, time.Time, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]db.QuotaCurrentRow, len(p.cache))
	copy(out, p.cache)
	return out, p.lastAt, p.apiCallAvail
}

func (p *Poller) Refresh() {
	select {
	case p.refreshCh <- struct{}{}:
	default:
	}
}

func jsonString(m map[string]interface{}, key string) string {
	v, ok := m[key].(string)
	if !ok {
		return ""
	}
	return v
}

func jsonFloat(m map[string]interface{}, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
