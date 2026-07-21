package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ai-gateway-metering-proxy/internal/cliproxy"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/credential"
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/metrics"
	"ai-gateway-metering-proxy/internal/pricing"
	"ai-gateway-metering-proxy/internal/proxy"
	"ai-gateway-metering-proxy/internal/quota"
	"ai-gateway-metering-proxy/internal/report"
	"ai-gateway-metering-proxy/internal/store"
	"ai-gateway-metering-proxy/internal/usagequeue"
	"ai-gateway-metering-proxy/internal/webui"
	"ai-gateway-metering-proxy/internal/writer"
)

const walSizeThreshold = 128 * 1024 * 1024 // 128 MiB

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	devStatic := flag.Bool("dev-static", false, "serve WebUI static files from disk (internal/webui/static/) for local development")
	seedDemo := flag.Bool("seed-demo", false, "insert demo data into the database for local WebUI testing")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// --seed-demo guard runs before any DB access to prevent touching the wrong database.
	if *seedDemo {
		if !*devStatic {
			log.Fatalf("--seed-demo requires --dev-static (refusing to seed without dev mode)")
		}
		dbPath := cfg.Database
		if filepath.IsAbs(dbPath) || strings.HasPrefix(dbPath, "/") || strings.HasPrefix(dbPath, `\`) ||
			!strings.HasSuffix(filepath.Base(dbPath), ".dev.sqlite") {
			log.Fatalf("--seed-demo refused: database %q is not a relative *.dev.sqlite path", dbPath)
		}
	}

	// Startup self-checks (fail-fast).

	if _, err := os.Stat(cfg.SaltFile); os.IsNotExist(err) {
		log.Fatalf("Salt file not found at %s. Generate one:\n  python3 -c \"import secrets; print(secrets.token_hex(32))\" > %s", cfg.SaltFile, cfg.SaltFile)
	}

	hasher, err := hash.New(cfg.SaltFile)
	if err != nil {
		log.Fatalf("Failed to load salt: %v", err)
	}

	// Check SQLite path is writable.
	database, err := db.Open(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Salt consistency guard (invariant #7): refuse to start if the salt file
	// has changed but the DB already has historical data, because that would
	// silently break all api_key_hash and client_ip_hash groupings.
	if err := database.VerifySaltFingerprint(hasher.SaltFingerprint(), cfg.Database, cfg.SaltFile); err != nil {
		log.Fatalf("Salt consistency check failed: %v", err)
	}

	// Check pricing file is parseable.
	pricingData, err := pricing.Load(cfg.PricingFile)
	if err != nil {
		log.Fatalf("Failed to load pricing file: %v", err)
	}

	if *seedDemo {
		if err := db.SeedDemo(database); err != nil {
			log.Fatalf("Failed to seed demo data: %v", err)
		}
		log.Printf("Demo data seeded successfully")
	}

	log.Printf("Startup self-check passed: salt=%s db=%s pricing=%s metering_enabled=%v",
		cfg.SaltFile, cfg.Database, cfg.PricingFile, cfg.MeteringEnabled)

	// Start batch writer.
	batchWriter := writer.New(store.NewEventSink(database), cfg.QueueCapacity, cfg.BatchSize, cfg.FlushInterval)
	batchWriter.Start()
	defer batchWriter.Stop()

	// Wire store interface boundaries.
	var reportStore store.ReportStore = database
	var healthWriter store.HealthWriter = database

	// Health metrics reporter (every 60s) with WAL checkpoint scheduling.
	stopHealth := make(chan struct{})
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		lastCheckpoint := time.Now()
		for {
			select {
			case <-ticker.C:
				qd, dropped, parseErrors, dbErrors := batchWriter.Snapshot()
				sseSkips := metrics.SSELineSkips()
				if err := healthWriter.InsertHealthMetric(time.Now().UTC().Format(time.RFC3339), int(qd), dropped, parseErrors, dbErrors, sseSkips); err != nil {
					log.Printf("health metric insert error: %v", err)
				}

				// Hourly PASSIVE WAL checkpoint.
				if time.Since(lastCheckpoint) >= time.Hour {
					result, err := database.CheckpointWAL("PASSIVE")
					if err != nil {
						log.Printf("wal checkpoint error: %v", err)
					} else {
						log.Printf("wal checkpoint: busy=%d log=%d checkpointed=%d",
							result.Busy, result.LogFrames, result.Checkpointed)
					}
					lastCheckpoint = time.Now()

					// If WAL file exceeds threshold, attempt TRUNCATE.
					walPath := database.Path() + "-wal"
					if info, statErr := os.Stat(walPath); statErr == nil {
						if info.Size() > walSizeThreshold {
							truncResult, truncErr := database.CheckpointWAL("TRUNCATE")
							if truncErr != nil {
								log.Printf("wal truncate checkpoint error: %v", truncErr)
							} else if truncResult.Busy == 0 {
								log.Printf("wal truncate checkpoint: busy=%d log=%d checkpointed=%d",
									truncResult.Busy, truncResult.LogFrames, truncResult.Checkpointed)
							}
						}
					}
				}
			case <-stopHealth:
				return
			}
		}
	}()
	defer close(stopHealth)

	// Create proxy handler.
	proxyHandler := proxy.New(cfg.Upstream, hasher, batchWriter, cfg.MaxNonstreamSampleBytes, cfg.RequestMetadata, cfg.ProxyTransport)
	proxyHandler.SetCorrelation(cfg.Observability.Correlation.Mode, cfg.Observability.Correlation.Header)
	proxyHandler.SetMeteringEnabled(cfg.MeteringEnabled)

	// Opt-in connection warmup. Default is off to avoid probing CPA on start.
	if cfg.ProxyTransport.WarmupOnStart {
		go func() {
			warmupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := proxyHandler.WarmupConnection(warmupCtx); err != nil {
				log.Printf("transport warmup failed (non-fatal): %v", err)
			}
		}()
	}

	// CLIProxyAPI management client and pollers.
	var credPoller *credential.Poller
	var quotaPoller *quota.Poller
	var usageQueuePoller *usagequeue.Poller
	if cfg.CLIProxyManagement.Enabled {
		managementKey, err := cliproxy.ReadKeyFile(cfg.CLIProxyManagement.KeyFile)
		if err != nil {
			log.Fatalf("Failed to read CLIProxyAPI management key: %v", err)
		}

		if cfg.CLIProxyManagement.UsageQueue.Enabled {
			allowRequestMerge := cfg.Observability.Correlation.RequirePropagationVerified &&
				cfg.Observability.Correlation.SideChannelMerge == "request_id" &&
				cfg.CLIProxyManagement.UsageQueue.MergeMode == "request_id"
			usageClient, err := cliproxy.NewClient(cliproxy.CLIProxyConfig{
				Enabled:   cfg.CLIProxyManagement.Enabled,
				BaseURL:   cfg.CLIProxyManagement.BaseURL,
				Key:       managementKey,
				Timeout:   cfg.CLIProxyManagement.UsageQueue.Timeout,
				Component: "usage_queue",
			})
			if err != nil {
				log.Fatalf("Failed to create CLIProxyAPI usage queue client: %v", err)
			}
			usageQueuePoller = usagequeue.NewHybridPoller(cfg.CLIProxyManagement.UsageQueue.RESPAddr, managementKey,
				usageClient, cfg.CLIProxyManagement.UsageQueue, database, hasher, allowRequestMerge)
			usageQueuePoller.Start()
			defer usageQueuePoller.Stop()
		}

		if cfg.CLIProxyManagement.CredentialHealth.Enabled {
			credClient, err := cliproxy.NewClient(cliproxy.CLIProxyConfig{
				Enabled:   cfg.CLIProxyManagement.Enabled,
				BaseURL:   cfg.CLIProxyManagement.BaseURL,
				Key:       managementKey,
				Timeout:   cfg.CLIProxyManagement.CredentialHealth.Timeout,
				Component: "credential_health",
			})
			if err != nil {
				log.Fatalf("Failed to create CLIProxyAPI credential client: %v", err)
			}
			credPoller = credential.NewPoller(credClient, database, hasher, cfg.CLIProxyManagement.CredentialHealth)
			credPoller.Start()
			defer credPoller.Stop()
		}

		if cfg.CLIProxyManagement.Quota.Enabled {
			quotaClient, err := cliproxy.NewClient(cliproxy.CLIProxyConfig{
				Enabled:   cfg.CLIProxyManagement.Enabled,
				BaseURL:   cfg.CLIProxyManagement.BaseURL,
				Key:       managementKey,
				Timeout:   cfg.CLIProxyManagement.Quota.Timeout,
				Component: "quota_refresh",
			})
			if err != nil {
				log.Fatalf("Failed to create CLIProxyAPI quota client: %v", err)
			}
			quotaPoller = quota.NewPoller(quotaClient, database, hasher, cfg.CLIProxyManagement.Quota)
			quotaPoller.Start()
			defer quotaPoller.Stop()
		}
	}

	// Set up mux.
	mux := http.NewServeMux()

	// /metrics endpoint for Prometheus.
	mux.Handle("/metrics", metrics.Handler())

	if cfg.WebUI.Enabled {
		modelsReporter := report.NewService(report.Dependencies{
			Models: database, Summary: database, Timeseries: database, Images: database,
			Overview: database, Capture: batchWriter,
		}, pricingData)
		var webuiServer *webui.Server
		if *devStatic {
			webuiServer = webui.NewWithStaticFS(reportStore, modelsReporter, pricingData, batchWriter, proxyHandler.Registry(), cfg.WebUI.BasePath, os.DirFS("internal/webui/static"))
		} else {
			webuiServer = webui.New(reportStore, modelsReporter, pricingData, batchWriter, proxyHandler.Registry(), cfg.WebUI.BasePath)
		}
		webuiServer.SetMeteringEnabledFunc(func() bool { return cfg.MeteringEnabled })
		webuiServer.SetCorrelationMode(cfg.Observability.Correlation.SideChannelMerge)
		if credPoller != nil {
			webuiServer.SetCredPoller(credPoller)
		}
		if quotaPoller != nil {
			webuiServer.SetQuotaPoller(quotaPoller)
		}
		if *seedDemo {
			if credPoller == nil {
				webuiServer.SetCredPoller(demoCredentialPoller{database: database})
			}
			if quotaPoller == nil {
				webuiServer.SetQuotaPoller(demoQuotaPoller{database: database})
			}
		}
		if usageQueuePoller != nil {
			webuiServer.SetUsageQueuePoller(usageQueuePoller)
		}
		mux.Handle(cfg.WebUI.BasePath, webuiServer)
		mux.Handle(cfg.WebUI.BasePath+"/", webuiServer)
	}

	// All other traffic goes to the proxy.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxyHandler.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Graceful shutdown timed out: %v", err)
			_ = srv.Close()
		}
	}()

	fmt.Printf("Metering Proxy listening on %s\n", cfg.Listen)
	fmt.Printf("Upstream: %s\n", cfg.Upstream)
	fmt.Printf("Database: %s\n", cfg.Database)
	fmt.Printf("WebUI: %s\n", cfg.WebUI.BasePath)
	fmt.Printf("Metering: %v\n", cfg.MeteringEnabled)

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("Server stopped")
}

type demoCredentialPoller struct {
	database *db.DB
}

func (p demoCredentialPoller) Snapshot() ([]db.CredentialHealthRow, time.Time) {
	rows, err := p.database.AllCredentialHealth()
	if err != nil {
		log.Printf("demo credential snapshot error: %v", err)
		return nil, time.Time{}
	}
	return rows, latestCredentialCheck(rows)
}

func (p demoCredentialPoller) Refresh() {}

func (p demoCredentialPoller) ResetCooldown() error {
	return fmt.Errorf("reset cooldown not available in demo mode")
}

type demoQuotaPoller struct {
	database *db.DB
}

func (p demoQuotaPoller) Snapshot() ([]db.QuotaCurrentRow, time.Time, bool) {
	rows, err := p.database.AllQuotaCurrent()
	if err != nil {
		log.Printf("demo quota snapshot error: %v", err)
		return nil, time.Time{}, false
	}
	return rows, latestQuotaCheck(rows), len(rows) > 0
}

func (p demoQuotaPoller) APICallAvailable() bool {
	rows, err := p.database.AllQuotaCurrent()
	return err == nil && len(rows) > 0
}

func (p demoQuotaPoller) Refresh() {}

func latestCredentialCheck(rows []db.CredentialHealthRow) time.Time {
	var latestUnix int64
	for _, row := range rows {
		if row.CheckedAtUnix > latestUnix {
			latestUnix = row.CheckedAtUnix
		}
	}
	if latestUnix <= 0 {
		return time.Time{}
	}
	return time.Unix(latestUnix, 0).UTC()
}

func latestQuotaCheck(rows []db.QuotaCurrentRow) time.Time {
	var latestUnix int64
	for _, row := range rows {
		if row.CheckedAtUnix > latestUnix {
			latestUnix = row.CheckedAtUnix
		}
	}
	if latestUnix <= 0 {
		return time.Time{}
	}
	return time.Unix(latestUnix, 0).UTC()
}
