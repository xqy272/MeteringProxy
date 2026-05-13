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

		row := db.CredentialHealthRow{
			Provider:       af.Provider,
			CredentialHash: credHash,
			AuthIndexHash:  authIndexHash,
			LabelHash:      labelHash,
			Status:         status,
			SuccessCount:   af.SuccessCount,
			FailedCount:    af.FailedCount,
			CheckedAt:      nowStr,
			CheckedAtUnix:  nowUnix,
			ErrorClass:     firstNonEmpty(af.ErrorClass, credentialErrorClass(status)),
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
		return "ready"
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

func credentialErrorClass(status string) string {
	switch status {
	case "", "active", "available", "ready", "unknown":
		return ""
	case "disabled":
		return "credential_disabled"
	case "stale":
		return "credential_stale"
	case "error":
		return "credential_error"
	default:
		return "credential_unavailable"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
