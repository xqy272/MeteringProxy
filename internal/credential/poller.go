package credential

import (
	"log"
	"strings"
	"sync"
	"time"

	"ai-gateway-metering-proxy/internal/cliproxy"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/store"
)

type Poller struct {
	client    *cliproxy.Client
	db        store.CredentialHealthStore
	hasher    *hash.Hasher
	cfg       config.CredentialHealthConfig
	mu        sync.RWMutex
	cache     []db.CredentialHealthRow
	lastAt    time.Time
	stopCh    chan struct{}
	doneCh    chan struct{}
	refreshCh chan struct{}
}

func NewPoller(client *cliproxy.Client, database store.CredentialHealthStore, hasher *hash.Hasher, cfg config.CredentialHealthConfig) *Poller {
	return &Poller{
		client:    client,
		db:        database,
		hasher:    hasher,
		cfg:       cfg,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		refreshCh: make(chan struct{}, 1),
	}
}

func (p *Poller) Start() {
	go p.loop()
}

func (p *Poller) Stop() {
	close(p.stopCh)
	<-p.doneCh
}

func (p *Poller) loop() {
	defer close(p.doneCh)
	p.poll()
	cleanupTicker := time.NewTicker(p.cfg.DiagnosticRetention)
	ticker := time.NewTicker(p.cfg.CacheTTL)
	defer ticker.Stop()
	defer cleanupTicker.Stop()
	for {
		select {
		case <-ticker.C:
			p.poll()
		case <-p.refreshCh:
			p.poll()
		case <-cleanupTicker.C:
			p.cleanup()
		case <-p.stopCh:
			return
		}
	}
}

func (p *Poller) cleanup() {
	cutoff := time.Now().Add(-p.cfg.DiagnosticRetention)
	if err := p.db.DeleteStaleCredentialHealth(cutoff); err != nil {
		log.Printf("credential health cleanup error: %v", err)
	}
}

func (p *Poller) poll() {
	resp, err := p.client.FetchAuthFiles()
	if err != nil {
		log.Printf("credential health poll error: %v", err)
		p.markExistingStale()
		return
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	nowUnix := now.Unix()

	var rows []db.CredentialHealthRow
	for _, af := range resp.AuthFiles {
		if af.Provider == "" && af.AuthType == "" && af.Key == "" && af.AuthIndex == "" && af.ID == "" {
			continue
		}
		credHash := ""
		if af.Key != "" {
			credHash = p.hashCredential(af.Provider, "key:"+af.Key)
		} else if af.AuthIndex != "" {
			credHash = p.hashCredential(af.Provider, "auth_index:"+fmtIndex(af.Provider, af.AuthType, af.AuthIndex))
		} else if af.ID != "" {
			credHash = p.hashCredential(af.Provider, "id:"+af.ID)
		} else if af.Source != "" {
			credHash = p.hashCredential(af.Provider, "source:"+af.Source)
		} else {
			credHash = p.hashCredential(af.Provider, "label:"+af.Label)
		}
		authIndexHash := ""
		if af.AuthIndex != "" {
			authIndexHash = p.hash("credential_auth_index:" + fmtIndex(af.Provider, af.AuthType, af.AuthIndex))
		}
		labelHash := ""
		if af.Label != "" {
			labelHash = p.hash("credential_label:" + af.Label)
		}
		status := normalizeCredentialStatus(af)
		displayLabel, identityHint := credentialIdentityLabels(af)

		row := db.CredentialHealthRow{
			Provider:           af.Provider,
			CredentialHash:     credHash,
			AuthIndexHash:      authIndexHash,
			LabelHash:          labelHash,
			DisplayLabel:       displayLabel,
			IdentityHint:       identityHint,
			Status:             status,
			StatusMessage:      af.StatusMessage,
			Plan:               af.Plan,
			SuccessCount:       af.SuccessCount,
			FailedCount:        af.FailedCount,
			RecentSuccessCount: af.RecentSuccessCount,
			RecentFailedCount:  af.RecentFailedCount,
			NextRetryAfter:     af.NextRetryAfter,
			NextRetryAfterUnix: af.NextRetryAfterUnix,
			CheckedAt:          nowStr,
			CheckedAtUnix:      nowUnix,
			ErrorClass:         credentialErrorClass(af, status),
			ErrorType:          af.ErrorType,
			ErrorCode:          af.ErrorCode,
			ErrorMessage:       af.ErrorMessage,
		}
		if err := p.db.UpsertCredentialHealth(&row); err != nil {
			log.Printf("credential health upsert error: %v", err)
		}
		rows = append(rows, row)
	}

	p.mu.Lock()
	p.cache = rows
	p.lastAt = now
	p.mu.Unlock()
}

func normalizeCredentialStatus(af cliproxy.AuthFileEntry) string {
	switch {
	case af.Disabled:
		return "disabled"
	case af.Unavailable:
		if credentialWarningFromCPAHistory(af) {
			return "warning"
		}
		return "unavailable"
	}
	status := strings.ToLower(strings.TrimSpace(af.Status))
	switch status {
	case "":
		if af.Available {
			return "ready"
		}
		return "unavailable"
	case "active", "available", "ready":
		if af.AvailableSet && !af.Available {
			if credentialWarningFromCPAHistory(af) {
				return "warning"
			}
			return "unavailable"
		}
		return "ready"
	case "error":
		if credentialWarningFromCPAHistory(af) {
			return "warning"
		}
		return "error"
	default:
		return status
	}
}

func (p *Poller) hash(value string) string {
	if p.hasher == nil {
		return ""
	}
	return p.hasher.Hash(value)
}

func (p *Poller) hashCredential(provider, material string) string {
	if p.hasher == nil {
		return ""
	}
	return p.hasher.Hash("credential:" + provider + ":" + material)
}

func credentialErrorClass(af cliproxy.AuthFileEntry, status string) string {
	switch status {
	case "", "active", "available", "ready", "unknown":
		return ""
	case "disabled":
		return "credential_disabled"
	case "stale":
		return "credential_stale"
	case "warning":
		return credentialWarningClass(af)
	case "unavailable":
		if containsQuotaSignal(credentialDiagnosticText(af)) {
			return "credential_quota_limited"
		}
		return firstNonEmpty(af.ErrorClass, "credential_unavailable")
	case "error":
		return firstNonEmpty(af.ErrorClass, "credential_error")
	default:
		return firstNonEmpty(af.ErrorClass, "credential_unavailable")
	}
}

func credentialWarningClass(af cliproxy.AuthFileEntry) string {
	if containsQuotaSignal(credentialDiagnosticText(af)) {
		return "credential_quota_limited"
	}
	return "credential_history_warning"
}

func credentialWarningFromCPAHistory(af cliproxy.AuthFileEntry) bool {
	text := credentialDiagnosticText(af)
	if containsAuthFailureSignal(text) {
		return false
	}
	if af.RecentSuccessCount > 0 && af.RecentSuccessCount >= af.RecentFailedCount {
		return true
	}
	if containsQuotaSignal(text) {
		return true
	}
	if !af.Available || af.SuccessCount <= 0 {
		return false
	}
	if af.FailedCount <= 0 {
		return true
	}
	return af.SuccessCount >= af.FailedCount*4
}

func credentialDiagnosticText(af cliproxy.AuthFileEntry) string {
	return strings.ToLower(strings.Join([]string{
		af.Status,
		af.StatusMessage,
		af.ErrorClass,
		af.ErrorType,
		af.ErrorCode,
		af.ErrorMessage,
		af.QuotaReason,
	}, " "))
}

func containsQuotaSignal(text string) bool {
	for _, needle := range []string{
		"usage_limit_reached",
		"usage limit",
		"quota",
		"rate_limit",
		"rate limit",
		"limit reached",
		"resets_at",
		"resets_in_seconds",
		"resource_exhausted",
		"too many requests",
	} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func containsAuthFailureSignal(text string) bool {
	for _, needle := range []string{
		"invalid_api_key",
		"invalid api key",
		"unauthorized",
		"authentication",
		"auth_failed",
		"forbidden",
		"revoked",
		"expired token",
		"token expired",
	} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func credentialIdentityLabels(af cliproxy.AuthFileEntry) (displayLabel, identityHint string) {
	displayLabel = safeCredentialDisplay(firstNonEmpty(af.Label, af.DisplayName, af.Name, af.Email))
	if email := safeEmail(af.Email); email != "" {
		identityHint = email
	} else if email := safeEmail(af.Name); email != "" {
		identityHint = email
	} else {
		identityHint = safeCredentialDisplay(firstNonEmpty(af.DisplayName, af.Name, af.Label))
	}
	if identityHint == displayLabel {
		identityHint = ""
	}
	return displayLabel, identityHint
}

func safeEmail(value string) string {
	value = safeCredentialDisplay(value)
	if value == "" || !strings.Contains(value, "@") {
		return ""
	}
	return value
}

func safeCredentialDisplay(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	for _, needle := range []string{"bearer ", "sk-", "sess-", "oauth", "token", "api_key", "apikey", "authorization"} {
		if strings.Contains(lower, needle) {
			return ""
		}
	}
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	if len(value) > 160 {
		runes := []rune(value)
		if len(runes) > 160 {
			value = string(runes[:160])
		}
	}
	return value
}

func (p *Poller) Snapshot() ([]db.CredentialHealthRow, time.Time) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]db.CredentialHealthRow, len(p.cache))
	copy(out, p.cache)
	return out, p.lastAt
}

func (p *Poller) Refresh() {
	select {
	case p.refreshCh <- struct{}{}:
	default:
	}
}

func (p *Poller) markExistingStale() {
	p.mu.RLock()
	rows := make([]db.CredentialHealthRow, len(p.cache))
	copy(rows, p.cache)
	p.mu.RUnlock()

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	nowUnix := now.Unix()
	for i := range rows {
		if rows[i].Status != "stale" && rows[i].Status != "error" {
			rows[i].Status = "stale"
			rows[i].CheckedAt = nowStr
			rows[i].CheckedAtUnix = nowUnix
			if err := p.db.UpsertCredentialHealth(&rows[i]); err != nil {
				log.Printf("credential health stale mark error: %v", err)
			}
		}
	}

	p.mu.Lock()
	p.cache = rows
	p.lastAt = now
	p.mu.Unlock()
}

func fmtIndex(provider, authType, authIndex string) string {
	return provider + ":" + authType + ":" + authIndex
}
