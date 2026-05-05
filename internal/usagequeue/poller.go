package usagequeue

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/db"
)

type Store interface {
	InsertSideUsageEvent(db.SideUsageEvent) (int64, error)
	ApplySideUsageEvent(int64, time.Duration) (string, error)
	DeleteStaleSideUsageEvents(time.Time) error
}

type Hasher interface {
	Hash(string) string
}

type Poller struct {
	addr              string
	key               string
	cfg               config.UsageQueueConfig
	store             Store
	hasher            Hasher
	allowRequestMerge bool

	stopCh chan struct{}
	doneCh chan struct{}

	mu        sync.RWMutex
	connected bool
	lastAt    time.Time
	lastErr   string
}

func NewPoller(addr, key string, cfg config.UsageQueueConfig, store Store, hasher Hasher, allowRequestMerge bool) *Poller {
	return &Poller{
		addr:              addr,
		key:               strings.TrimSpace(key),
		cfg:               cfg,
		store:             store,
		hasher:            hasher,
		allowRequestMerge: allowRequestMerge,
		stopCh:            make(chan struct{}),
		doneCh:            make(chan struct{}),
	}
}

func (p *Poller) Start() {
	go p.run()
}

func (p *Poller) Stop() {
	close(p.stopCh)
	<-p.doneCh
}

func (p *Poller) Snapshot() (connected bool, lastAt time.Time, lastErr string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.connected, p.lastAt, p.lastErr
}

func (p *Poller) run() {
	defer close(p.doneCh)
	pollInterval := p.cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	reconnectInterval := p.cfg.ReconnectInterval
	if reconnectInterval <= 0 {
		reconnectInterval = 5 * time.Second
	}
	for {
		select {
		case <-p.stopCh:
			return
		default:
		}
		if err := p.consumeLoop(pollInterval); err != nil {
			p.setState(false, err)
			timer := time.NewTimer(reconnectInterval)
			select {
			case <-p.stopCh:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

func (p *Poller) consumeLoop(pollInterval time.Duration) error {
	conn, err := net.DialTimeout("tcp", p.addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if p.key != "" {
		if err := writeRESPCommand(conn, "AUTH", p.key); err != nil {
			return err
		}
		if _, _, err := readRESP(reader); err != nil {
			return err
		}
	}
	p.setState(true, nil)
	for {
		select {
		case <-p.stopCh:
			return nil
		default:
		}
		processed, err := p.pollBatch(conn, reader)
		if err != nil {
			return err
		}
		p.cleanup()
		if processed == 0 {
			timer := time.NewTimer(pollInterval)
			select {
			case <-p.stopCh:
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
}

func (p *Poller) pollBatch(conn net.Conn, reader *bufio.Reader) (int, error) {
	batchSize := p.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	pop := strings.ToUpper(strings.TrimSpace(p.cfg.Pop))
	if pop == "" {
		pop = "LPOP"
	}
	queue := p.cfg.QueueName
	if queue == "" {
		queue = "queue"
	}
	processed := 0
	for i := 0; i < batchSize; i++ {
		if err := writeRESPCommand(conn, pop, queue); err != nil {
			return processed, err
		}
		payload, nilBulk, err := readRESP(reader)
		if err != nil {
			return processed, err
		}
		if nilBulk {
			return processed, nil
		}
		if err := p.processPayload(payload); err != nil {
			log.Printf("usage queue payload error: %v", err)
		}
		processed++
		p.setLastAt(time.Now())
	}
	return processed, nil
}

func (p *Poller) processPayload(payload []byte) error {
	event := p.parsePayload(payload)
	id, err := p.store.InsertSideUsageEvent(event)
	if err != nil {
		return err
	}
	if !p.allowRequestMerge || event.MatchStatus == "invalid_payload" || event.RequestID == "" {
		return nil
	}
	_, err = p.store.ApplySideUsageEvent(id, p.cfg.MatchTimeout)
	return err
}

func (p *Poller) parsePayload(payload []byte) db.SideUsageEvent {
	now := time.Now().UTC()
	event := db.SideUsageEvent{
		ReceivedAt:     now.Format(time.RFC3339),
		ReceivedAtUnix: now.Unix(),
		MatchStatus:    "stored_only",
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		event.MatchStatus = "invalid_payload"
		event.ErrorClass = "invalid_payload"
		return event
	}
	event.Provider = stringField(raw, "provider")
	event.Model = stringField(raw, "model")
	event.Alias = stringField(raw, "alias")
	event.Endpoint = stringField(raw, "endpoint", "path")
	event.AuthType = stringField(raw, "auth_type")
	event.RequestID = stringField(raw, "request_id", "requestId")
	event.ErrorClass = stringField(raw, "error_class")
	event.AuthIndexHash = p.hashSensitive(raw, "auth_index_hash", "auth_index")
	event.SourceHash = p.hashSensitive(raw, "source_hash", "source")
	event.APIKeyHash = p.hashSensitive(raw, "api_key_hash", "api_key")
	event.InputTokens = int64Field(raw, "input_tokens", "prompt_tokens")
	event.OutputTokens = int64Field(raw, "output_tokens", "completion_tokens")
	event.ReasoningTokens = int64Field(raw, "reasoning_tokens")
	event.CachedTokens = int64Field(raw, "cached_tokens")
	event.TotalTokens = int64Field(raw, "total_tokens")
	if event.TotalTokens == 0 && (event.InputTokens > 0 || event.OutputTokens > 0) {
		event.TotalTokens = event.InputTokens + event.OutputTokens
	}
	event.LatencyMs = int64Field(raw, "latency_ms", "duration_ms")
	event.Failed = boolIntField(raw, "failed", "error")
	return event
}

func (p *Poller) hashSensitive(raw map[string]any, hashedKey, rawKey string) string {
	if v := stringField(raw, hashedKey); v != "" {
		return v
	}
	if v := stringField(raw, rawKey); v != "" && p.hasher != nil {
		return p.hasher.Hash(v)
	}
	return ""
}

func (p *Poller) cleanup() {
	if p.cfg.EventRetention <= 0 {
		return
	}
	if err := p.store.DeleteStaleSideUsageEvents(time.Now().Add(-p.cfg.EventRetention)); err != nil {
		log.Printf("usage queue retention cleanup error: %v", err)
	}
}

func (p *Poller) setState(connected bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connected = connected
	if err != nil {
		p.lastErr = err.Error()
	} else {
		p.lastErr = ""
	}
}

func (p *Poller) setLastAt(t time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAt = t
}

func writeRESPCommand(w io.Writer, args ...string) error {
	if _, err := fmt.Fprintf(w, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(w, "$%d\r\n%s\r\n", len(arg), arg); err != nil {
			return err
		}
	}
	return nil
}

func readRESP(r *bufio.Reader) ([]byte, bool, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, false, err
	}
	switch prefix {
	case '+':
		line, err := readRESPLine(r)
		return []byte(line), false, err
	case '-':
		line, _ := readRESPLine(r)
		return nil, false, fmt.Errorf("resp error: %s", line)
	case '$':
		line, err := readRESPLine(r)
		if err != nil {
			return nil, false, err
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, false, err
		}
		if n < 0 {
			return nil, true, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, false, err
		}
		if len(buf) < 2 || buf[len(buf)-2] != '\r' || buf[len(buf)-1] != '\n' {
			return nil, false, fmt.Errorf("invalid bulk string terminator")
		}
		return buf[:n], false, nil
	default:
		return nil, false, fmt.Errorf("unsupported resp prefix %q", prefix)
	}
}

func readRESPLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func stringField(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := raw[key]
		if !ok || v == nil {
			continue
		}
		switch typed := v.(type) {
		case string:
			if s := strings.TrimSpace(typed); s != "" {
				return s
			}
		case json.Number:
			return typed.String()
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		case bool:
			if typed {
				return "true"
			}
			return "false"
		}
	}
	return ""
}

func int64Field(raw map[string]any, keys ...string) int64 {
	for _, key := range keys {
		v, ok := raw[key]
		if !ok || v == nil {
			continue
		}
		switch typed := v.(type) {
		case float64:
			return int64(typed)
		case string:
			n, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
			return n
		case json.Number:
			n, _ := typed.Int64()
			return n
		}
	}
	return 0
}

func boolIntField(raw map[string]any, keys ...string) int64 {
	for _, key := range keys {
		v, ok := raw[key]
		if !ok || v == nil {
			continue
		}
		switch typed := v.(type) {
		case bool:
			if typed {
				return 1
			}
			return 0
		case float64:
			if typed != 0 {
				return 1
			}
		case string:
			s := strings.ToLower(strings.TrimSpace(typed))
			if s == "true" || s == "1" || s == "yes" || s == "error" {
				return 1
			}
		}
	}
	return 0
}
