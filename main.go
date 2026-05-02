package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/pricing"
	"ai-gateway-metering-proxy/internal/proxy"
	"ai-gateway-metering-proxy/internal/webui"
	"ai-gateway-metering-proxy/internal/writer"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Ensure salt exists
	if _, err := os.Stat(cfg.SaltFile); os.IsNotExist(err) {
		log.Fatalf("Salt file not found at %s. Generate one:\n  python3 -c \"import secrets; print(secrets.token_hex(32))\" > %s", cfg.SaltFile, cfg.SaltFile)
	}

	hasher, err := hash.New(cfg.SaltFile)
	if err != nil {
		log.Fatalf("Failed to load salt: %v", err)
	}

	// Open database
	database, err := db.Open(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Load pricing
	pricingData, err := pricing.Load(cfg.PricingFile)
	if err != nil {
		log.Printf("Warning: failed to load pricing file: %v (cost display will be limited)", err)
		pricingData = &pricing.Pricing{Models: make(map[string]pricing.ModelPrice)}
	}

	// Start batch writer
	batchWriter := writer.New(database, cfg.QueueCapacity, cfg.BatchSize, cfg.FlushInterval)
	batchWriter.Start()
	defer batchWriter.Stop()

	// Health metrics reporter (every 60s)
	stopHealth := make(chan struct{})
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				qd, dropped, parseErrors, dbErrors := batchWriter.Snapshot()
				if err := database.InsertHealthMetric(time.Now().UTC().Format(time.RFC3339), int(qd), dropped, parseErrors, dbErrors); err != nil {
					log.Printf("health metric insert error: %v", err)
				}
			case <-stopHealth:
				return
			}
		}
	}()
	defer close(stopHealth)

	// Create proxy handler
	proxyHandler := proxy.New(cfg.Upstream, hasher, batchWriter, cfg.MaxNonstreamSampleBytes)

	// Set up mux
	mux := http.NewServeMux()

	if cfg.WebUI.Enabled {
		webuiServer := webui.New(database, pricingData, batchWriter, cfg.WebUI.BasePath)
		mux.Handle(cfg.WebUI.BasePath, webuiServer)
		mux.Handle(cfg.WebUI.BasePath+"/", webuiServer)
	}

	// All other traffic goes to the proxy
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

	// Graceful shutdown
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

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("Server stopped")
}
