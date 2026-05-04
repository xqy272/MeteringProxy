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
