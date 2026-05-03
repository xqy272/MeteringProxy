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

	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/metrics"
	"ai-gateway-metering-proxy/internal/pricing"
	"ai-gateway-metering-proxy/internal/proxy"
	"ai-gateway-metering-proxy/internal/store"
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
	proxyHandler := proxy.New(cfg.Upstream, hasher, batchWriter, cfg.MaxNonstreamSampleBytes)
	proxyHandler.SetMeteringEnabled(cfg.MeteringEnabled)

	// Set up mux.
	mux := http.NewServeMux()

	// /metrics endpoint for Prometheus.
	mux.Handle("/metrics", metrics.Handler())

	if cfg.WebUI.Enabled {
		var webuiServer *webui.Server
		if *devStatic {
			webuiServer = webui.NewWithStaticFS(reportStore, pricingData, batchWriter, proxyHandler.Registry(), cfg.WebUI.BasePath, os.DirFS("internal/webui/static"))
		} else {
			webuiServer = webui.New(reportStore, pricingData, batchWriter, proxyHandler.Registry(), cfg.WebUI.BasePath)
		}
		webuiServer.SetMeteringEnabledFunc(func() bool { return cfg.MeteringEnabled })
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
