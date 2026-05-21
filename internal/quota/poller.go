package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
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
	client        *cliproxy.Client
	database      store.QuotaStore
	hasher        *hash.Hasher
	cfg           config.QuotaConfig
	mu            sync.RWMutex
	cache         []db.QuotaCurrentRow
	lastAt        time.Time
	apiCallAvail  bool
	probeStatus   int
	probeClass    string
	probeEndpoint string
	stopCh        chan struct{}
	doneCh        chan struct{}
	refreshCh     chan struct{}
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
	const endpoint = "/api-call"
	if p.client == nil {
		p.mu.Lock()
		p.apiCallAvail = false
		p.probeStatus = 0
		p.probeClass = "api_call_disabled"
		p.probeEndpoint = endpoint
		p.mu.Unlock()
		return
	}
	body := bytes.NewReader([]byte(`{"method":"GET","url":"http://127.0.0.1:0/__metering_probe__"}`))
	respBody, statusCode, err := p.client.DoAPICall("POST", endpoint, body)
	class := classifyAPICallProbe(statusCode, respBody, err)
	available := apiCallProbeReachable(statusCode, respBody, err)
	if err != nil {
		log.Printf("quota api-call probe error: %v", err)
	}
	p.mu.Lock()
	p.apiCallAvail = available
	p.probeStatus = statusCode
	p.probeClass = class
	p.probeEndpoint = endpoint
	p.mu.Unlock()
}

func (p *Poller) APICallAvailable() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.apiCallAvail
}

func (p *Poller) probeSnapshot() (bool, int, string, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.apiCallAvail, p.probeStatus, p.probeClass, p.probeEndpoint
}

func apiCallProbeReachable(statusCode int, body []byte, err error) bool {
	if err != nil {
		return false
	}
	if statusCode >= 200 && statusCode < 300 {
		return true
	}
	// CPA commonly returns 400 for a syntactically valid endpoint with an
	// unsupported probe body. That proves the management route exists, but not
	// that full quota snapshots are available.
	if statusCode == http.StatusBadRequest {
		return true
	}
	if statusCode == http.StatusBadGateway && bytes.Contains(bytes.ToLower(body), []byte("request failed")) {
		return true
	}
	return false
}

func classifyAPICallProbe(statusCode int, body []byte, err error) string {
	if err != nil {
		return "api_call_transport_error"
	}
	switch statusCode {
	case 0:
		return "api_call_no_response"
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		return "api_call_ok"
	case http.StatusBadRequest:
		return "api_call_bad_request"
	case http.StatusUnauthorized:
		return "api_call_unauthorized"
	case http.StatusForbidden:
		return "api_call_forbidden"
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return "api_call_not_found"
	case http.StatusBadGateway:
		if bytes.Contains(bytes.ToLower(body), []byte("request failed")) {
			return "api_call_probe_reachable"
		}
		return "api_call_bad_gateway"
	case http.StatusServiceUnavailable:
		return "api_call_unavailable"
	case http.StatusGatewayTimeout:
		return "api_call_timeout"
	}
	if statusCode >= 500 {
		return "api_call_server_error"
	}
	if statusCode >= 400 {
		return "api_call_client_error"
	}
	return "api_call_unexpected_status"
}

func classifyProviderAPICallStatus(statusCode int, body []byte) string {
	text := strings.ToLower(string(bytes.TrimSpace(body)))
	switch statusCode {
	case http.StatusBadRequest:
		if strings.Contains(text, "missing method") || strings.Contains(text, "missing url") {
			return "api_call_contract_error"
		}
		return "provider_bad_request"
	case http.StatusUnauthorized:
		return "provider_unauthorized"
	case http.StatusForbidden:
		return "provider_forbidden"
	case http.StatusNotFound:
		return "provider_not_found"
	case http.StatusTooManyRequests:
		return "provider_rate_limited"
	case http.StatusBadGateway:
		return "provider_bad_gateway"
	case http.StatusServiceUnavailable:
		return "provider_unavailable"
	case http.StatusGatewayTimeout:
		return "provider_timeout"
	}
	if statusCode >= 500 {
		return "provider_server_error"
	}
	if statusCode >= 400 {
		return "provider_client_error"
	}
	return "provider_error"
}

func (p *Poller) poll() {
	if !p.cfg.Enabled {
		return
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	nowUnix := now.Unix()
	apiCallAvailable, _, probeClass, _ := p.probeSnapshot()
	if !apiCallAvailable {
		if probeClass == "" {
			probeClass = "api_call_unavailable"
		}
		for _, provider := range p.cfg.Providers {
			p.recordRefreshEvent(provider, "", "probe", "error", probeClass, 0, nowStr, nowUnix)
			p.markProviderStale(provider, probeClass, nowStr, nowUnix)
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
		p.recordRefreshEvent(provider, "", "api_call", "error", "api_call_transport_error", durationMs, nowStr, nowUnix)
		p.markProviderStale(provider, "api_call_transport_error", nowStr, nowUnix)
		return
	}
	if statusCode != 200 {
		class := classifyProviderAPICallStatus(statusCode, respBody)
		p.recordRefreshEvent(provider, "", "api_call", "error", class, durationMs, nowStr, nowUnix)
		p.markProviderStale(provider, class, nowStr, nowUnix)
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

func adapterForProvider(_ string) (ProviderAdapter, bool) {
	return nil, false
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
		if errorClass == "unsupported" {
			errorClass = "quota_unsupported"
		}
	}
	apiCallReachable, probeHTTPStatus, probeClass, probeEndpoint := p.probeSnapshot()
	providerSupported := int64(0)
	if status == "success" || adapterStatus == "available" {
		providerSupported = 1
	}
	reachable := int64(0)
	if apiCallReachable {
		reachable = 1
	}
	details, _ := json.Marshal(map[string]any{
		"api_call_reachable": apiCallReachable,
		"probe_status":       probeClass,
		"event":              adapterStatus,
	})
	row := &db.QuotaRefreshEventRow{
		CheckedAt:         nowStr,
		CheckedAtUnix:     nowUnix,
		Provider:          provider,
		CredentialHash:    credHash,
		Phase:             phase,
		Status:            status,
		AdapterStatus:     adapterStatus,
		DurationMs:        durationMs,
		ErrorClass:        errorClass,
		ProbeHTTPStatus:   probeHTTPStatus,
		ProbeEndpoint:     probeEndpoint,
		ProbeErrorClass:   probeClass,
		APICallReachable:  reachable,
		ProviderSupported: providerSupported,
		DetailsJSON:       string(details),
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
