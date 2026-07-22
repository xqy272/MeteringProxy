package config

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen                  string        `yaml:"listen"`
	Upstream                string        `yaml:"upstream"`
	Database                string        `yaml:"database"`
	SaltFile                string        `yaml:"salt_file"`
	QueueCapacity           int           `yaml:"queue_capacity"`
	BatchSize               int           `yaml:"batch_size"`
	FlushInterval           time.Duration `yaml:"flush_interval"`
	MaxNonstreamSampleBytes int64         `yaml:"max_nonstream_sample_bytes"`
	MeteringEnabled         bool          `yaml:"metering_enabled"`
	WebUI                   WebUIConfig   `yaml:"webui"`
	PricingFile             string        `yaml:"pricing_file"`
	// KeyLabels maps a full API key HMAC-SHA256 hash (64 lowercase hex) to a
	// display label. Loaded at process start only; missing or empty maps are
	// valid and leave all keys unlabeled.
	KeyLabels          map[string]string        `yaml:"key_labels"`
	RequestMetadata    RequestMetadataConfig    `yaml:"request_metadata"`
	ProxyTransport     ProxyTransportConfig     `yaml:"proxy_transport"`
	Observability      ObservabilityConfig      `yaml:"observability"`
	CLIProxyManagement CLIProxyManagementConfig `yaml:"cliproxy_management"`
}

type WebUIConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BasePath string `yaml:"base_path"`
}

type RequestMetadataConfig struct {
	InitialBytes      int64 `yaml:"initial_bytes"`
	MaxBytes          int64 `yaml:"max_bytes"`
	ExtendedModelScan bool  `yaml:"extended_model_scan"`
}

// ProxyTransportConfig tunes the HTTP transport used to forward requests to
// the upstream. Defaults preserve the previous hard-coded behavior; every
// field is optional and clamped to a safe range in Load.
type ProxyTransportConfig struct {
	MaxIdleConns          int           `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost   int           `yaml:"max_idle_conns_per_host"`
	IdleConnTimeout       time.Duration `yaml:"idle_conn_timeout"`
	TLSHandshakeTimeout   time.Duration `yaml:"tls_handshake_timeout"`
	ExpectContinueTimeout time.Duration `yaml:"expect_continue_timeout"`
	ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"`
	ForceHTTP2            bool          `yaml:"force_http2"`
	WarmupOnStart         bool          `yaml:"warmup_on_start"`
}

type ObservabilityConfig struct {
	Correlation CorrelationConfig `yaml:"correlation"`
}

type CorrelationConfig struct {
	Mode                       string `yaml:"mode"`
	Header                     string `yaml:"header"`
	SideChannelMerge           string `yaml:"side_channel_merge"`
	RequirePropagationVerified bool   `yaml:"require_propagation_verified"`
}

type CLIProxyManagementConfig struct {
	Enabled          bool                   `yaml:"enabled"`
	BaseURL          string                 `yaml:"base_url"`
	KeyFile          string                 `yaml:"key_file"`
	UsageQueue       UsageQueueConfig       `yaml:"usage_queue"`
	CredentialHealth CredentialHealthConfig `yaml:"credential_health"`
	Quota            QuotaConfig            `yaml:"quota"`
}

type UsageQueueConfig struct {
	Enabled           bool          `yaml:"enabled"`
	Transport         string        `yaml:"transport"`
	RESPAddr          string        `yaml:"resp_addr"`
	QueueName         string        `yaml:"queue_name"`
	Pop               string        `yaml:"pop"`
	BatchSize         int           `yaml:"batch_size"`
	Timeout           time.Duration `yaml:"timeout"`
	PollInterval      time.Duration `yaml:"poll_interval"`
	ReconnectInterval time.Duration `yaml:"reconnect_interval"`
	MatchTimeout      time.Duration `yaml:"match_timeout"`
	EventRetention    time.Duration `yaml:"event_retention"`
	MergeMode         string        `yaml:"merge_mode"`
}

type CredentialHealthConfig struct {
	Enabled             bool          `yaml:"enabled"`
	CacheTTL            time.Duration `yaml:"cache_ttl"`
	Timeout             time.Duration `yaml:"timeout"`
	DiagnosticRetention time.Duration `yaml:"diagnostic_retention"`
	// MinRefreshInterval debounces manual auth-files refresh calls. Default 30s.
	MinRefreshInterval time.Duration `yaml:"min_refresh_interval"`
	// RefreshMode controls background polling. "manual" (default) means
	// refresh only on explicit POST; no startup or periodic polling.
	RefreshMode string `yaml:"refresh_mode"`
}

type QuotaConfig struct {
	Enabled             bool          `yaml:"enabled"`
	Providers           []string      `yaml:"providers"`
	CacheTTL            time.Duration `yaml:"cache_ttl"`
	Timeout             time.Duration `yaml:"timeout"`
	Concurrency         int           `yaml:"concurrency"`
	DiagnosticRetention time.Duration `yaml:"diagnostic_retention"`
	LowThreshold        float64       `yaml:"low_threshold"`
	WarningThreshold    float64       `yaml:"warning_threshold"`
	RetryMinInterval    time.Duration `yaml:"retry_min_interval"`
	RetryMaxInterval    time.Duration `yaml:"retry_max_interval"`
	// MinRefreshInterval debounces manual refresh calls so rapid WebUI clicks
	// do not flood CPA with /api-call requests. Default 60s.
	MinRefreshInterval time.Duration `yaml:"min_refresh_interval"`
	// RefreshMode controls whether the poller runs in the background.
	// "manual" (default) means refresh only on explicit POST; no background
	// polling. "scheduled" is reserved for a future explicit background mode.
	RefreshMode string `yaml:"refresh_mode"`
}

const (
	minQueueCapacity           = 100
	maxQueueCapacity           = 1_000_000
	maxBatchSize               = 10_000
	maxFlushInterval           = time.Minute
	minNonstreamSampleBytes    = 1024
	defaultNonstreamSampleSize = 2 * 1024 * 1024
	maxNonstreamSampleBytes    = 64 * 1024 * 1024

	minProxyIdleConns        = 0
	maxProxyIdleConns        = 10_000
	minProxyIdleConnsPerHost = 0
	maxProxyIdleConnsPerHost = 10_000
	maxProxyIdleTimeout      = 1 * time.Hour
	maxProxyHandshakeTimeout = 5 * time.Minute

	maxKeyLabelRunes = 80
)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	// Runtime serve path keeps historical Unmarshal behavior for compatibility.
	return parseConfig(data, false)
}

// LoadStrict loads config with strict YAML decoding: unknown fields and multiple
// documents are rejected. Used by the validate preflight command only.
func LoadStrict(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return parseConfig(data, true)
}

func parseConfig(data []byte, strict bool) (*Config, error) {
	cfg := &Config{
		Listen:                  "127.0.0.1:8320",
		Upstream:                "http://127.0.0.1:8317",
		Database:                "/opt/ai-gateway/metering/usage.sqlite",
		SaltFile:                "/opt/ai-gateway/metering/salt",
		QueueCapacity:           1000,
		BatchSize:               50,
		FlushInterval:           1 * time.Second,
		MaxNonstreamSampleBytes: 2 * 1024 * 1024,
		MeteringEnabled:         true,
		PricingFile:             "/opt/ai-gateway/metering/pricing.yaml",
		WebUI: WebUIConfig{
			Enabled:  true,
			BasePath: "/metering",
		},
		RequestMetadata: RequestMetadataConfig{
			InitialBytes:      4096,
			MaxBytes:          65536,
			ExtendedModelScan: false,
		},
		ProxyTransport: ProxyTransportConfig{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 0,
			ForceHTTP2:            false,
			WarmupOnStart:         false,
		},
		Observability: ObservabilityConfig{
			Correlation: CorrelationConfig{
				Mode:                       "passive",
				Header:                     "X-Request-ID",
				SideChannelMerge:           "stored_only",
				RequirePropagationVerified: true,
			},
		},
		CLIProxyManagement: CLIProxyManagementConfig{
			Enabled: false,
			BaseURL: "http://127.0.0.1:8317/v0/management",
			UsageQueue: UsageQueueConfig{
				Enabled:           false,
				Transport:         "auto",
				RESPAddr:          "127.0.0.1:8317",
				QueueName:         "queue",
				Pop:               "LPOP",
				BatchSize:         50,
				Timeout:           10 * time.Second,
				PollInterval:      1 * time.Second,
				ReconnectInterval: 5 * time.Second,
				MatchTimeout:      10 * time.Minute,
				EventRetention:    7 * 24 * time.Hour,
				MergeMode:         "stored_only",
			},
			CredentialHealth: CredentialHealthConfig{
				Enabled:             true,
				CacheTTL:            5 * time.Minute,
				Timeout:             10 * time.Second,
				DiagnosticRetention: 72 * time.Hour,
			},
			Quota: QuotaConfig{
				Enabled:             false,
				Providers:           []string{"claude", "codex", "kimi"},
				CacheTTL:            5 * time.Minute,
				Timeout:             10 * time.Second,
				Concurrency:         4,
				DiagnosticRetention: 72 * time.Hour,
				LowThreshold:        0.2,
				WarningThreshold:    0.5,
				RetryMinInterval:    30 * time.Second,
				RetryMaxInterval:    10 * time.Minute,
			},
		},
	}
	if strict {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil {
			if err != io.EOF {
				return nil, fmt.Errorf("parse config: %w", err)
			}
		}
		var extra yaml.Node
		if err := dec.Decode(&extra); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("parse config: multiple YAML documents are not allowed")
			}
			return nil, fmt.Errorf("parse config: %w", err)
		}
	} else if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.Upstream = strings.TrimRight(strings.TrimSpace(cfg.Upstream), "/")
	if err := validateUpstream(cfg.Upstream); err != nil {
		return nil, err
	}
	if cfg.QueueCapacity < minQueueCapacity {
		cfg.QueueCapacity = minQueueCapacity
	} else if cfg.QueueCapacity > maxQueueCapacity {
		cfg.QueueCapacity = maxQueueCapacity
	}
	if cfg.BatchSize < 1 {
		cfg.BatchSize = 50
	} else if cfg.BatchSize > maxBatchSize {
		cfg.BatchSize = maxBatchSize
	}
	if cfg.BatchSize > cfg.QueueCapacity {
		cfg.BatchSize = cfg.QueueCapacity
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 1 * time.Second
	} else if cfg.FlushInterval > maxFlushInterval {
		cfg.FlushInterval = maxFlushInterval
	}
	if cfg.MaxNonstreamSampleBytes < minNonstreamSampleBytes {
		cfg.MaxNonstreamSampleBytes = defaultNonstreamSampleSize
	} else if cfg.MaxNonstreamSampleBytes > maxNonstreamSampleBytes {
		cfg.MaxNonstreamSampleBytes = maxNonstreamSampleBytes
	}
	if cfg.WebUI.Enabled {
		if cfg.WebUI.BasePath == "" {
			cfg.WebUI.BasePath = "/metering"
		}
		if !strings.HasPrefix(cfg.WebUI.BasePath, "/") {
			cfg.WebUI.BasePath = "/" + cfg.WebUI.BasePath
		}
		cfg.WebUI.BasePath = strings.TrimRight(cfg.WebUI.BasePath, "/")
		if cfg.WebUI.BasePath == "" {
			return nil, fmt.Errorf("webui.base_path must not be / because it would intercept proxied API traffic")
		}
	}
	if cfg.RequestMetadata.InitialBytes <= 0 {
		cfg.RequestMetadata.InitialBytes = 4096
	}
	if cfg.RequestMetadata.InitialBytes > 65536 {
		cfg.RequestMetadata.InitialBytes = 65536
	}
	if cfg.RequestMetadata.MaxBytes < cfg.RequestMetadata.InitialBytes {
		cfg.RequestMetadata.MaxBytes = cfg.RequestMetadata.InitialBytes
	}
	if cfg.RequestMetadata.MaxBytes > 65536 {
		cfg.RequestMetadata.MaxBytes = 65536
	}
	clampProxyTransport(&cfg.ProxyTransport)
	if cfg.Observability.Correlation.Mode == "" {
		cfg.Observability.Correlation.Mode = "passive"
	}
	if cfg.Observability.Correlation.Header == "" {
		cfg.Observability.Correlation.Header = "X-Request-ID"
	}
	if cfg.Observability.Correlation.SideChannelMerge == "" {
		cfg.Observability.Correlation.SideChannelMerge = "stored_only"
	}
	if cfg.Observability.Correlation.Mode != "passive" && cfg.Observability.Correlation.Mode != "inject_if_missing" {
		return nil, fmt.Errorf("observability.correlation.mode must be passive or inject_if_missing")
	}
	if cfg.Observability.Correlation.SideChannelMerge != "stored_only" && cfg.Observability.Correlation.SideChannelMerge != "request_id" {
		return nil, fmt.Errorf("observability.correlation.side_channel_merge must be stored_only or request_id")
	}
	if cfg.CLIProxyManagement.UsageQueue.PollInterval <= 0 {
		cfg.CLIProxyManagement.UsageQueue.PollInterval = 1 * time.Second
	}
	if cfg.CLIProxyManagement.UsageQueue.ReconnectInterval <= 0 {
		cfg.CLIProxyManagement.UsageQueue.ReconnectInterval = 5 * time.Second
	}
	if cfg.CLIProxyManagement.UsageQueue.BatchSize <= 0 {
		cfg.CLIProxyManagement.UsageQueue.BatchSize = 50
	}
	if cfg.CLIProxyManagement.UsageQueue.Timeout <= 0 {
		cfg.CLIProxyManagement.UsageQueue.Timeout = 10 * time.Second
	}
	if cfg.CLIProxyManagement.UsageQueue.Pop == "" {
		cfg.CLIProxyManagement.UsageQueue.Pop = "LPOP"
	}
	if cfg.CLIProxyManagement.UsageQueue.Transport == "" {
		cfg.CLIProxyManagement.UsageQueue.Transport = "auto"
	}
	if cfg.CLIProxyManagement.UsageQueue.QueueName == "" {
		cfg.CLIProxyManagement.UsageQueue.QueueName = "queue"
	}
	if cfg.CLIProxyManagement.UsageQueue.MergeMode == "" {
		cfg.CLIProxyManagement.UsageQueue.MergeMode = "stored_only"
	}
	cfg.CLIProxyManagement.UsageQueue.Pop = strings.ToUpper(strings.TrimSpace(cfg.CLIProxyManagement.UsageQueue.Pop))
	if cfg.CLIProxyManagement.UsageQueue.Pop != "LPOP" && cfg.CLIProxyManagement.UsageQueue.Pop != "RPOP" {
		return nil, fmt.Errorf("cliproxy_management.usage_queue.pop must be LPOP or RPOP")
	}
	cfg.CLIProxyManagement.UsageQueue.Transport = strings.ToLower(strings.TrimSpace(cfg.CLIProxyManagement.UsageQueue.Transport))
	if cfg.CLIProxyManagement.UsageQueue.Transport != "auto" && cfg.CLIProxyManagement.UsageQueue.Transport != "http" && cfg.CLIProxyManagement.UsageQueue.Transport != "resp" {
		return nil, fmt.Errorf("cliproxy_management.usage_queue.transport must be auto, http, or resp")
	}
	if cfg.CLIProxyManagement.UsageQueue.MergeMode != "stored_only" && cfg.CLIProxyManagement.UsageQueue.MergeMode != "request_id" {
		return nil, fmt.Errorf("cliproxy_management.usage_queue.merge_mode must be stored_only or request_id")
	}
	if cfg.CLIProxyManagement.CredentialHealth.CacheTTL <= 0 {
		cfg.CLIProxyManagement.CredentialHealth.CacheTTL = 5 * time.Minute
	}
	if cfg.CLIProxyManagement.CredentialHealth.Timeout <= 0 {
		cfg.CLIProxyManagement.CredentialHealth.Timeout = 10 * time.Second
	}
	if cfg.CLIProxyManagement.CredentialHealth.DiagnosticRetention <= 0 {
		cfg.CLIProxyManagement.CredentialHealth.DiagnosticRetention = 72 * time.Hour
	}
	if cfg.CLIProxyManagement.CredentialHealth.MinRefreshInterval <= 0 {
		cfg.CLIProxyManagement.CredentialHealth.MinRefreshInterval = 30 * time.Second
	}
	cfg.CLIProxyManagement.CredentialHealth.RefreshMode = strings.ToLower(strings.TrimSpace(cfg.CLIProxyManagement.CredentialHealth.RefreshMode))
	if cfg.CLIProxyManagement.CredentialHealth.RefreshMode == "" {
		cfg.CLIProxyManagement.CredentialHealth.RefreshMode = "manual"
	}
	if cfg.CLIProxyManagement.CredentialHealth.RefreshMode != "manual" && cfg.CLIProxyManagement.CredentialHealth.RefreshMode != "scheduled" {
		return nil, fmt.Errorf("cliproxy_management.credential_health.refresh_mode must be manual or scheduled")
	}
	if cfg.CLIProxyManagement.Quota.CacheTTL <= 0 {
		cfg.CLIProxyManagement.Quota.CacheTTL = 5 * time.Minute
	}
	if cfg.CLIProxyManagement.Quota.Timeout <= 0 {
		cfg.CLIProxyManagement.Quota.Timeout = 10 * time.Second
	}
	if cfg.CLIProxyManagement.Quota.Concurrency <= 0 {
		cfg.CLIProxyManagement.Quota.Concurrency = 4
	}
	if cfg.CLIProxyManagement.Quota.DiagnosticRetention <= 0 {
		cfg.CLIProxyManagement.Quota.DiagnosticRetention = 72 * time.Hour
	}
	if cfg.CLIProxyManagement.Quota.LowThreshold <= 0 || cfg.CLIProxyManagement.Quota.LowThreshold > 1 {
		cfg.CLIProxyManagement.Quota.LowThreshold = 0.2
	}
	if cfg.CLIProxyManagement.Quota.WarningThreshold <= 0 || cfg.CLIProxyManagement.Quota.WarningThreshold > 1 {
		cfg.CLIProxyManagement.Quota.WarningThreshold = 0.5
	}
	if cfg.CLIProxyManagement.Quota.RetryMinInterval <= 0 {
		cfg.CLIProxyManagement.Quota.RetryMinInterval = 30 * time.Second
	}
	if cfg.CLIProxyManagement.Quota.RetryMaxInterval <= 0 {
		cfg.CLIProxyManagement.Quota.RetryMaxInterval = 10 * time.Minute
	}
	if cfg.CLIProxyManagement.Quota.MinRefreshInterval <= 0 {
		cfg.CLIProxyManagement.Quota.MinRefreshInterval = 60 * time.Second
	}
	cfg.CLIProxyManagement.Quota.RefreshMode = strings.ToLower(strings.TrimSpace(cfg.CLIProxyManagement.Quota.RefreshMode))
	if cfg.CLIProxyManagement.Quota.RefreshMode == "" {
		cfg.CLIProxyManagement.Quota.RefreshMode = "manual"
	}
	if cfg.CLIProxyManagement.Quota.RefreshMode != "manual" && cfg.CLIProxyManagement.Quota.RefreshMode != "scheduled" {
		return nil, fmt.Errorf("cliproxy_management.quota.refresh_mode must be manual or scheduled")
	}
	if cfg.CLIProxyManagement.Enabled {
		if err := validateCLIProxyManagement(cfg.CLIProxyManagement); err != nil {
			return nil, err
		}
	}
	if err := normalizeKeyLabels(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func validateUpstream(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("upstream URL is invalid: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("upstream URL must use http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("upstream URL must include a host")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("upstream URL must not include query or fragment")
	}
	return nil
}

// ClampProxyTransport fills missing fields with defaults and clamps configured
// values to safe ranges. Zero-value fields inherit the previous hard-coded
// behavior so existing deployments are unchanged.
func ClampProxyTransport(t *ProxyTransportConfig) {
	clampProxyTransport(t)
}

// clampProxyTransport fills missing fields with defaults and clamps configured
// values to safe ranges. Zero-value fields inherit the previous hard-coded
// behavior so existing deployments are unchanged.
func clampProxyTransport(t *ProxyTransportConfig) {
	if t.MaxIdleConns < minProxyIdleConns {
		t.MaxIdleConns = 100
	} else if t.MaxIdleConns > maxProxyIdleConns {
		t.MaxIdleConns = maxProxyIdleConns
	}
	if t.MaxIdleConnsPerHost < minProxyIdleConnsPerHost {
		t.MaxIdleConnsPerHost = 20
	} else if t.MaxIdleConnsPerHost > maxProxyIdleConnsPerHost {
		t.MaxIdleConnsPerHost = maxProxyIdleConnsPerHost
	}
	if t.IdleConnTimeout <= 0 {
		t.IdleConnTimeout = 90 * time.Second
	} else if t.IdleConnTimeout > maxProxyIdleTimeout {
		t.IdleConnTimeout = maxProxyIdleTimeout
	}
	if t.TLSHandshakeTimeout <= 0 {
		t.TLSHandshakeTimeout = 10 * time.Second
	} else if t.TLSHandshakeTimeout > maxProxyHandshakeTimeout {
		t.TLSHandshakeTimeout = maxProxyHandshakeTimeout
	}
	if t.ExpectContinueTimeout < 0 {
		t.ExpectContinueTimeout = 1 * time.Second
	}
	if t.ResponseHeaderTimeout < 0 {
		t.ResponseHeaderTimeout = 0
	}
}

func validateCLIProxyManagement(cfg CLIProxyManagementConfig) error {
	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil {
		return fmt.Errorf("cliproxy_management.base_url is invalid: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("cliproxy_management.base_url must use http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("cliproxy_management.base_url must include a host")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("cliproxy_management.base_url must not include query or fragment")
	}
	if strings.TrimRight(u.Path, "/") != "/v0/management" {
		return fmt.Errorf("cliproxy_management.base_url must end with /v0/management")
	}
	if strings.TrimSpace(cfg.KeyFile) == "" {
		return fmt.Errorf("cliproxy_management.key_file is required when management is enabled")
	}
	if _, err := os.Stat(cfg.KeyFile); err != nil {
		return fmt.Errorf("cliproxy_management.key_file is not readable: %w", err)
	}
	return nil
}

// normalizeKeyLabels validates and trims key_labels into a stable map of
// full 64-char lowercase hex hashes to non-empty display labels.
func normalizeKeyLabels(cfg *Config) error {
	if cfg.KeyLabels == nil || len(cfg.KeyLabels) == 0 {
		cfg.KeyLabels = map[string]string{}
		return nil
	}

	normalized := make(map[string]string, len(cfg.KeyLabels))
	for hash, label := range cfg.KeyLabels {
		path := keyLabelsPath(hash)
		if hash == "unknown" {
			return fmt.Errorf("%s: hash must not be unknown", path)
		}
		if !isFullLowerHexHash(hash) {
			return fmt.Errorf("%s: hash must be exactly 64 lowercase hex characters", path)
		}
		trimmed := strings.TrimSpace(label)
		if trimmed == "" {
			return fmt.Errorf("%s: label must not be empty after trim", path)
		}
		if labelHasControlRune(trimmed) {
			return fmt.Errorf("%s: label must not contain control characters", path)
		}
		if utf8.RuneCountInString(trimmed) > maxKeyLabelRunes {
			return fmt.Errorf("%s: label must not exceed %d characters", path, maxKeyLabelRunes)
		}
		normalized[hash] = trimmed
	}
	cfg.KeyLabels = normalized
	return nil
}

func keyLabelsPath(hash string) string {
	if hash == "unknown" {
		return "key_labels.unknown"
	}
	if isFullLowerHexHash(hash) {
		return "key_labels." + hash
	}
	// An invalid map key may be an accidentally pasted plaintext API key.
	// Never echo it in validation errors.
	return "key_labels[invalid_hash]"
}

func isFullLowerHexHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func labelHasControlRune(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
