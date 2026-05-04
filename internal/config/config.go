package config

import (
	"fmt"
	"net/url"
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
	MeteringEnabled         bool          `yaml:"metering_enabled"`
	WebUI                   WebUIConfig   `yaml:"webui"`
	PricingFile             string        `yaml:"pricing_file"`
}

type WebUIConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BasePath string `yaml:"base_path"`
}

const (
	minQueueCapacity           = 100
	maxQueueCapacity           = 1_000_000
	maxBatchSize               = 10_000
	maxFlushInterval           = time.Minute
	minNonstreamSampleBytes    = 1024
	defaultNonstreamSampleSize = 2 * 1024 * 1024
	maxNonstreamSampleBytes    = 64 * 1024 * 1024
)

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
		MeteringEnabled:         true,
		PricingFile:             "/opt/ai-gateway/metering/pricing.yaml",
		WebUI: WebUIConfig{
			Enabled:  true,
			BasePath: "/metering",
		},
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
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
