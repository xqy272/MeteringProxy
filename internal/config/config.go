package config

import (
	"fmt"
	"os"
	"strings"
	"time"

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
	WebUI                   WebUIConfig   `yaml:"webui"`
	PricingFile             string        `yaml:"pricing_file"`
}

type WebUIConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BasePath string `yaml:"base_path"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{
		Listen:                  "127.0.0.1:8320",
		Upstream:                "http://127.0.0.1:8317",
		Database:                "/opt/ai-gateway/metering/usage.sqlite",
		SaltFile:                "/opt/ai-gateway/metering/salt",
		QueueCapacity:           1000,
		BatchSize:               50,
		FlushInterval:           1 * time.Second,
		MaxNonstreamSampleBytes: 2 * 1024 * 1024,
		PricingFile:             "/opt/ai-gateway/metering/pricing.yaml",
		WebUI: WebUIConfig{
			Enabled:  true,
			BasePath: "/metering",
		},
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.QueueCapacity < 100 {
		cfg.QueueCapacity = 100
	}
	if cfg.BatchSize < 1 {
		cfg.BatchSize = 50
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 1 * time.Second
	}
	if cfg.MaxNonstreamSampleBytes < 1024 {
		cfg.MaxNonstreamSampleBytes = 2 * 1024 * 1024
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
	return cfg, nil
}
