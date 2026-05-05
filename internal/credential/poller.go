package credential

import (
	"log"
	"sync"
	"time"

	"ai-gateway-metering-proxy/internal/cliproxy"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/hash"
)

type Poller struct {
	client    *cliproxy.Client
	db        *db.DB
	hasher    *hash.Hasher
	cfg       config.CredentialHealthConfig
	mu        sync.RWMutex
	cache     []db.CredentialHealthRow
	lastAt    time.Time
	stopCh    chan struct{}
	refreshCh chan struct{}
}

func NewPoller(client *cliproxy.Client, database *db.DB, hasher *hash.Hasher, cfg config.CredentialHealthConfig) *Poller {
	return &Poller{
		client:    client,
		db:        database,
		hasher:    hasher,
		cfg:       cfg,
		stopCh:    make(chan struct{}),
		refreshCh: make(chan struct{}, 1),
	}
}

func (p *Poller) Start() {
	go p.loop()
}

func (p *Poller) Stop() {
	close(p.stopCh)
}

func (p *Poller) loop() {
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
		if af.Provider == "" && af.AuthType == "" && af.Key == "" {
			continue
		}
		credHash := ""
		if af.Key != "" {
			credHash = p.hasher.Hash(af.Provider + ":" + af.Key)
		} else {
			credHash = p.hasher.Hash(fmtIndex(af.Provider, af.AuthType, af.AuthIndex))
		}
		authIndexHash := p.hasher.Hash(fmtIndex(af.Provider, af.AuthType, af.AuthIndex))
		labelHash := ""
		if af.Label != "" {
			labelHash = p.hasher.Hash(af.Label)
		}
		status := af.Status
		if status == "" {
			if af.Available {
				status = "ready"
			} else {
				status = "unavailable"
			}
		}

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

func fmtIndex(provider, authType string, authIndex int) string {
	return provider + ":" + authType + ":" + intToStr(authIndex)
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
