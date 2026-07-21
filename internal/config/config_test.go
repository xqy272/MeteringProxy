package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

const (
	validKeyHashA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	validKeyHashB = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
)

func TestLoadKeyLabelsMissingFieldCompatible(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.KeyLabels == nil {
		t.Fatal("KeyLabels = nil, want empty map")
	}
	if len(cfg.KeyLabels) != 0 {
		t.Fatalf("KeyLabels len = %d, want 0", len(cfg.KeyLabels))
	}
}

func TestLoadKeyLabelsEmptyMapCompatible(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
key_labels: {}
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.KeyLabels == nil {
		t.Fatal("KeyLabels = nil, want empty map")
	}
	if len(cfg.KeyLabels) != 0 {
		t.Fatalf("KeyLabels len = %d, want 0", len(cfg.KeyLabels))
	}
}

func TestLoadKeyLabelsValidMapping(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
key_labels:
  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "friend-a"
  "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789": "self"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.KeyLabels[validKeyHashA]; got != "friend-a" {
		t.Fatalf("label A = %q, want friend-a", got)
	}
	if got := cfg.KeyLabels[validKeyHashB]; got != "self" {
		t.Fatalf("label B = %q, want self", got)
	}
}

func TestLoadKeyLabelsTrimsLabel(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
key_labels:
  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "  friend-a  "
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.KeyLabels[validKeyHashA]; got != "friend-a" {
		t.Fatalf("label = %q, want friend-a", got)
	}
}

func TestLoadKeyLabelsAllowsDuplicateLabels(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
key_labels:
  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "shared"
  "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789": "shared"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.KeyLabels[validKeyHashA] != "shared" || cfg.KeyLabels[validKeyHashB] != "shared" {
		t.Fatalf("KeyLabels = %#v, want both shared", cfg.KeyLabels)
	}
}

func TestLoadKeyLabelsRejectsUnknownHash(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
key_labels:
  "unknown": "nope"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown hash")
	}
	if !strings.Contains(err.Error(), "key_labels.unknown") {
		t.Fatalf("error = %v, want path key_labels.unknown", err)
	}
}

func TestLoadKeyLabelsRejectsHashLength63And65(t *testing.T) {
	for _, n := range []int{63, 65} {
		hash := strings.Repeat("a", n)
		body := "upstream: \"http://127.0.0.1:8317\"\nkey_labels:\n  \"" + hash + "\": \"label\"\n"
		path := writeConfig(t, body)
		_, err := Load(path)
		if err == nil {
			t.Fatalf("expected error for hash len %d", n)
		}
		if !strings.Contains(err.Error(), "64 lowercase hex") {
			t.Fatalf("error = %v, want 64 lowercase hex message", err)
		}
	}
}

func TestLoadKeyLabelsRejectsUppercaseHash(t *testing.T) {
	upper := strings.ToUpper(validKeyHashA)
	body := "upstream: \"http://127.0.0.1:8317\"\nkey_labels:\n  \"" + upper + "\": \"label\"\n"
	path := writeConfig(t, body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for uppercase hash")
	}
	if !strings.Contains(err.Error(), "key_labels[invalid_hash]") || strings.Contains(err.Error(), upper) {
		t.Fatalf("error = %v, want redacted stable path", err)
	}
}

func TestLoadKeyLabelsRejectsNonHexHash(t *testing.T) {
	bad := strings.Repeat("g", 64)
	body := "upstream: \"http://127.0.0.1:8317\"\nkey_labels:\n  \"" + bad + "\": \"label\"\n"
	path := writeConfig(t, body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for non-hex hash")
	}
	if !strings.Contains(err.Error(), "key_labels[invalid_hash]") || strings.Contains(err.Error(), bad) {
		t.Fatalf("error = %v, want redacted stable path", err)
	}
}

func TestLoadKeyLabelsRejectsWhitespaceWrappedHash(t *testing.T) {
	body := "upstream: \"http://127.0.0.1:8317\"\nkey_labels:\n  \" " + validKeyHashA + " \": \"label\"\n"
	path := writeConfig(t, body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for whitespace-wrapped hash")
	}
	if !strings.Contains(err.Error(), "key_labels") {
		t.Fatalf("error = %v, want key_labels path", err)
	}
	if !strings.Contains(err.Error(), "64 lowercase hex") {
		t.Fatalf("error = %v, want hash format message", err)
	}
}

func TestLoadKeyLabelsRejectsEmptyLabel(t *testing.T) {
	path := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
key_labels:
  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "   "
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty label")
	}
	if !strings.Contains(err.Error(), "key_labels."+validKeyHashA) {
		t.Fatalf("error = %v, want stable path", err)
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error = %v, want empty label message", err)
	}
}

func TestLoadKeyLabelsRejectsControlRune(t *testing.T) {
	// YAML double-quoted scalar turns \t into a real tab control rune.
	body := "upstream: \"http://127.0.0.1:8317\"\nkey_labels:\n  \"" + validKeyHashA + "\": \"bad\\tlabel\"\n"
	path := writeConfig(t, body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for control rune in label")
	}
	if !strings.Contains(err.Error(), "key_labels."+validKeyHashA) {
		t.Fatalf("error = %v, want stable path", err)
	}
	if !strings.Contains(err.Error(), "control") {
		t.Fatalf("error = %v, want control character message", err)
	}
}

func TestLoadKeyLabelsAccepts80RunesRejects81(t *testing.T) {
	label80 := strings.Repeat("\u754c", 80)
	if utf8.RuneCountInString(label80) != 80 {
		t.Fatalf("fixture rune count = %d", utf8.RuneCountInString(label80))
	}
	bodyOK := "upstream: \"http://127.0.0.1:8317\"\nkey_labels:\n  \"" + validKeyHashA + "\": \"" + label80 + "\"\n"
	pathOK := writeConfig(t, bodyOK)
	cfg, err := Load(pathOK)
	if err != nil {
		t.Fatalf("Load 80-rune label: %v", err)
	}
	if got := cfg.KeyLabels[validKeyHashA]; got != label80 {
		t.Fatalf("label not preserved for 80 runes")
	}

	label81 := strings.Repeat("\u754c", 81)
	bodyBad := "upstream: \"http://127.0.0.1:8317\"\nkey_labels:\n  \"" + validKeyHashA + "\": \"" + label81 + "\"\n"
	pathBad := writeConfig(t, bodyBad)
	_, err = Load(pathBad)
	if err == nil {
		t.Fatal("expected error for 81-rune label")
	}
	if !strings.Contains(err.Error(), "key_labels."+validKeyHashA) {
		t.Fatalf("error = %v, want stable path", err)
	}
	if !strings.Contains(err.Error(), "80") {
		t.Fatalf("error = %v, want 80-character limit message", err)
	}
}

func TestLoadKeyLabelsErrorsDoNotLeakCredentials(t *testing.T) {
	secretSalt := "super-secret-salt-value-do-not-leak"
	secretMgmtKey := "mgmt-key-plaintext-should-not-appear"
	plaintextAPIKey := "sk-live-plaintext-api-key-must-not-appear"
	body := "upstream: \"http://127.0.0.1:8317\"\n" +
		"salt_file: \"/data/" + secretSalt + "\"\n" +
		"cliproxy_management:\n" +
		"  enabled: false\n" +
		"  key_file: \"/secrets/" + secretMgmtKey + "\"\n" +
		"key_labels:\n" +
		"  \"unknown\": \"nope\"\n"
	path := writeConfig(t, body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := err.Error()
	if strings.Contains(msg, secretSalt) {
		t.Fatalf("error leaked salt path secret: %v", err)
	}
	if strings.Contains(msg, secretMgmtKey) {
		t.Fatalf("error leaked management key path secret: %v", err)
	}
	if !strings.Contains(msg, "key_labels.unknown") {
		t.Fatalf("error = %v, want stable key_labels path", err)
	}

	plaintextPath := writeConfig(t, `
upstream: "http://127.0.0.1:8317"
key_labels:
  "`+plaintextAPIKey+`": "nope"
`)
	_, err = Load(plaintextPath)
	if err == nil {
		t.Fatal("expected validation error for plaintext map key")
	}
	if strings.Contains(err.Error(), plaintextAPIKey) {
		t.Fatalf("error leaked plaintext API key: %v", err)
	}
	if !strings.Contains(err.Error(), "key_labels[invalid_hash]") {
		t.Fatalf("error = %v, want redacted stable key_labels path", err)
	}
}
