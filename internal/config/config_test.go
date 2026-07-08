package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadDefaultsPricingAndNormalizesWebBasePath(t *testing.T) {
	path := writeConfig(t, `
listen: "127.0.0.1:9000"
upstream: "http://127.0.0.1:8317/"
webui:
  enabled: true
  base_path: "stats/"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PricingFile != "/opt/ai-gateway/metering/pricing.yaml" {
		t.Errorf("PricingFile = %q", cfg.PricingFile)
	}
	if cfg.WebUI.BasePath != "/stats" {
		t.Errorf("BasePath = %q, want /stats", cfg.WebUI.BasePath)
	}
	if cfg.Upstream != "http://127.0.0.1:8317" {
		t.Errorf("Upstream = %q, want trimmed upstream URL", cfg.Upstream)
	}
	if cfg.FlushInterval != time.Second {
		t.Errorf("FlushInterval = %v, want 1s", cfg.FlushInterval)
	}
}

func TestLoadRejectsRootWebBasePath(t *testing.T) {
	path := writeConfig(t, `
webui:
  enabled: true
  base_path: "/"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for root webui.base_path")
	}
}

func TestLoadRejectsInvalidUpstream(t *testing.T) {
	for _, body := range []string{
		`upstream: "127.0.0.1:8317"`,
		`upstream: "file:///tmp/socket"`,
		`upstream: "http://127.0.0.1:8317?x=1"`,
	} {
		path := writeConfig(t, body)
		if _, err := Load(path); err == nil {
			t.Fatalf("expected invalid upstream error for %s", body)
		}
	}
}

func TestLoadClampsNumericBounds(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
queue_capacity: 999999999
batch_size: 999999999
flush_interval: 24h
max_nonstream_sample_bytes: 999999999
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.QueueCapacity != maxQueueCapacity {
		t.Fatalf("QueueCapacity = %d, want %d", cfg.QueueCapacity, maxQueueCapacity)
	}
	if cfg.BatchSize != maxBatchSize {
		t.Fatalf("BatchSize = %d, want %d", cfg.BatchSize, maxBatchSize)
	}
	if cfg.FlushInterval != maxFlushInterval {
		t.Fatalf("FlushInterval = %s, want %s", cfg.FlushInterval, maxFlushInterval)
	}
	if cfg.MaxNonstreamSampleBytes != maxNonstreamSampleBytes {
		t.Fatalf("MaxNonstreamSampleBytes = %d, want %d", cfg.MaxNonstreamSampleBytes, maxNonstreamSampleBytes)
	}
}

func TestLoadProxyTransportDefaultsPreserveBehavior(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pt := cfg.ProxyTransport
	if pt.MaxIdleConns != 100 {
		t.Errorf("MaxIdleConns = %d, want 100", pt.MaxIdleConns)
	}
	if pt.MaxIdleConnsPerHost != 20 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 20", pt.MaxIdleConnsPerHost)
	}
	if pt.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", pt.IdleConnTimeout)
	}
	if pt.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 10s", pt.TLSHandshakeTimeout)
	}
	if pt.ExpectContinueTimeout != 1*time.Second {
		t.Errorf("ExpectContinueTimeout = %v, want 1s", pt.ExpectContinueTimeout)
	}
	if pt.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout = %v, want 0 (streaming-safe)", pt.ResponseHeaderTimeout)
	}
	if pt.ForceHTTP2 {
		t.Errorf("ForceHTTP2 = true, want false (preserve current behavior)")
	}
	if pt.WarmupOnStart {
		t.Errorf("WarmupOnStart = true, want false (opt-in only)")
	}
}

func TestLoadProxyTransportClamps(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
proxy_transport:
  max_idle_conns: 999999
  max_idle_conns_per_host: 999999
  idle_conn_timeout: 24h
  tls_handshake_timeout: 2h
  expect_continue_timeout: -5s
  response_header_timeout: -5s
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pt := cfg.ProxyTransport
	if pt.MaxIdleConns != maxProxyIdleConns {
		t.Errorf("MaxIdleConns = %d, want %d", pt.MaxIdleConns, maxProxyIdleConns)
	}
	if pt.MaxIdleConnsPerHost != maxProxyIdleConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost = %d, want %d", pt.MaxIdleConnsPerHost, maxProxyIdleConnsPerHost)
	}
	if pt.IdleConnTimeout != maxProxyIdleTimeout {
		t.Errorf("IdleConnTimeout = %v, want %v", pt.IdleConnTimeout, maxProxyIdleTimeout)
	}
	if pt.TLSHandshakeTimeout != maxProxyHandshakeTimeout {
		t.Errorf("TLSHandshakeTimeout = %v, want %v", pt.TLSHandshakeTimeout, maxProxyHandshakeTimeout)
	}
	if pt.ExpectContinueTimeout != 1*time.Second {
		t.Errorf("ExpectContinueTimeout = %v, want 1s (negative clamped to default)", pt.ExpectContinueTimeout)
	}
	if pt.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout = %v, want 0 (negative clamped to 0)", pt.ResponseHeaderTimeout)
	}
}

func TestLoadProxyTransportAcceptsExplicitValues(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
proxy_transport:
  max_idle_conns: 200
  max_idle_conns_per_host: 32
  idle_conn_timeout: 120s
  force_http2: true
  warmup_on_start: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pt := cfg.ProxyTransport
	if pt.MaxIdleConns != 200 {
		t.Errorf("MaxIdleConns = %d, want 200", pt.MaxIdleConns)
	}
	if pt.MaxIdleConnsPerHost != 32 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 32", pt.MaxIdleConnsPerHost)
	}
	if pt.IdleConnTimeout != 120*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 120s", pt.IdleConnTimeout)
	}
	if !pt.ForceHTTP2 {
		t.Errorf("ForceHTTP2 = false, want true")
	}
	if !pt.WarmupOnStart {
		t.Errorf("WarmupOnStart = false, want true")
	}
}

func TestLoadAllowsInjectIfMissingCorrelationMode(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
observability:
  correlation:
    mode: "inject_if_missing"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Observability.Correlation.Mode != "inject_if_missing" {
		t.Fatalf("mode = %q", cfg.Observability.Correlation.Mode)
	}
}

func TestLoadRequestMetadataClampKeepsMaxAtLeastInitial(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
request_metadata:
  initial_bytes: 70000
  max_bytes: 65536
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RequestMetadata.InitialBytes != 65536 || cfg.RequestMetadata.MaxBytes != 65536 {
		t.Fatalf("request metadata = initial %d max %d, want 65536/65536", cfg.RequestMetadata.InitialBytes, cfg.RequestMetadata.MaxBytes)
	}
}

func TestLoadUsageQueueTransportDefaultsToAuto(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
cliproxy_management:
  usage_queue:
    enabled: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CLIProxyManagement.UsageQueue.Transport != "auto" {
		t.Fatalf("transport = %q, want auto", cfg.CLIProxyManagement.UsageQueue.Transport)
	}
	if cfg.CLIProxyManagement.UsageQueue.Timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", cfg.CLIProxyManagement.UsageQueue.Timeout)
	}
}

func TestLoadQuotaDefaultsToDisabledUntilProviderAdaptersAreConfigured(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CLIProxyManagement.Quota.Enabled {
		t.Fatal("quota.enabled default = true, want false")
	}
}

func TestLoadRejectsInvalidUsageQueueTransport(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
cliproxy_management:
  usage_queue:
    transport: "smtp"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected invalid usage_queue.transport error")
	}
}
