package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"ai-gateway-metering-proxy/internal/cliproxy"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/hash"
)

type Poller struct {
	client       *cliproxy.Client
	database     *db.DB
	hasher       *hash.Hasher
	cfg          config.QuotaConfig
	mu           sync.RWMutex
	cache        []db.QuotaCurrentRow
	lastAt       time.Time
	apiCallAvail bool
	stopCh       chan struct{}
	refreshCh    chan struct{}
}

func NewPoller(client *cliproxy.Client, database *db.DB, hasher *hash.Hasher, cfg config.QuotaConfig) *Poller {
	return &Poller{
		client:    client,
		database:  database,
		hasher:    hasher,
		cfg:       cfg,
		stopCh:    make(chan struct{}),
		refreshCh: make(chan struct{}, 1),
	}
}

func (p *Poller) Start() {
	go p.probeAndLoop()
}

func (p *Poller) Stop() {
	close(p.stopCh)
}

func (p *Poller) probeAndLoop() {
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
	body := bytes.NewReader([]byte(`{"provider":"__metering_probe__","dry_run":true}`))
	_, statusCode, err := p.client.DoAPICall("POST", "/api-call", body)
	if err != nil {
		p.mu.Lock()
		p.apiCallAvail = false
		p.mu.Unlock()
		return
	}
	p.mu.Lock()
	p.apiCallAvail = (statusCode >= 200 && statusCode < 300) || statusCode == http.StatusBadRequest
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
			p.markProviderStale(provider, nowStr, nowUnix)
		}
		p.refreshDBState()
		return
	}

	for _, provider := range p.cfg.Providers {
		p.pollProvider(provider, nowStr, nowUnix)
	}

	p.refreshDBState()
}

func (p *Poller) pollProvider(provider, nowStr string, nowUnix int64) {
	path := "/api-call"
	body := buildQuotaRequestBody(provider)
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
		p.markProviderStale(provider, nowStr, nowUnix)
		return
	}
	if statusCode != 200 {
		p.recordRefreshEvent(provider, "", "api_call", "error", "provider_error", durationMs, nowStr, nowUnix)
		p.markProviderStale(provider, nowStr, nowUnix)
		return
	}
	p.parseQuotaResponse(provider, respBody, nowStr, nowUnix)
	p.recordRefreshEvent(provider, "", "api_call", "success", "available", durationMs, nowStr, nowUnix)
}

func (p *Poller) markProviderStale(provider, nowStr string, nowUnix int64) {
	rows, err := p.database.AllQuotaCurrent()
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.Provider == provider && row.Status != "stale" && row.Status != "error" {
			row.Status = "stale"
			row.AdapterStatus = "unreachable"
			row.CheckedAt = nowStr
			row.CheckedAtUnix = nowUnix
			if err := p.database.UpsertQuotaCurrent(&row); err != nil {
				log.Printf("quota stale mark error for %s/%s: %v", provider, row.CredentialHash, err)
			}
		}
	}
}

func (p *Poller) parseQuotaResponse(provider string, body []byte, nowStr string, nowUnix int64) {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		log.Printf("quota poll: parse error for %s: %v", provider, err)
		p.recordRefreshEvent(provider, "", "parse", "error", "parse_error", 0, nowStr, nowUnix)
		return
	}
	data, ok := raw["data"].([]interface{})
	if !ok {
		single, ok := raw["data"].(map[string]interface{})
		if ok {
			data = []interface{}{single}
		} else {
			return
		}
	}
	for _, item := range data {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		p.processQuotaEntry(provider, entry, nowStr, nowUnix)
	}
}

func (p *Poller) processQuotaEntry(provider string, entry map[string]interface{}, nowStr string, nowUnix int64) {
	credHash := ""
	if key, ok := entry["key"].(string); ok && key != "" {
		credHash = p.hasher.Hash(provider + ":" + key)
	} else if h, ok := entry["credential_hash"].(string); ok && h != "" {
		credHash = h
	} else if idx, ok := entry["auth_index"]; ok {
		credHash = p.hasher.Hash(provider + ":auth_index:" + fmt.Sprint(idx))
	} else if source, ok := entry["source"].(string); ok && source != "" {
		credHash = p.hasher.Hash(provider + ":source:" + source)
	}
	windowKey := ""
	if wk, ok := entry["window_key"].(string); ok {
		windowKey = wk
	}
	if windowKey == "" {
		windowKey = "default"
	}
	if credHash == "" {
		log.Printf("quota poll: skipping %s quota entry without credential identity", provider)
		p.recordRefreshEvent(provider, "", "parse", "error", "missing_credential_identity", 0, nowStr, nowUnix)
		return
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
	}
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
	row := &db.QuotaRefreshEventRow{
		CheckedAt:      nowStr,
		CheckedAtUnix:  nowUnix,
		Provider:       provider,
		CredentialHash: credHash,
		Phase:          phase,
		Status:         status,
		AdapterStatus:  adapterStatus,
		DurationMs:     durationMs,
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

func buildQuotaRequestBody(provider string) map[string]interface{} {
	return map[string]interface{}{
		"provider": provider,
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
