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
