package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const readinessProbeTimeout = 2 * time.Second

// componentStatus uses low-cardinality values only.
const (
	componentOK    = "ok"
	componentError = "error"
)

// dbReadyProbe probes SQLite read/write handles without touching external services.
type dbReadyProbe interface {
	Ready(ctx context.Context) error
}

// writerReadyProbe reports whether the async writer loop has been started and not stopped.
type writerReadyProbe interface {
	Running() bool
}

// readiness holds process-local readiness inputs established at serve startup.
// It never references upstream, provider, CPA management, or quota clients.
type readiness struct {
	configOK     bool
	saltOK       bool
	pricingOK    bool
	db           dbReadyProbe
	writer       writerReadyProbe
	probeTimeout time.Duration
}

type healthzResponse struct {
	Status string `json:"status"`
}

type readyzResponse struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
}

func healthzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeStableJSON(w, http.StatusOK, healthzResponse{Status: "ok"})
	})
}

func readyzHandler(state *readiness) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if state == nil {
			writeStableJSON(w, http.StatusServiceUnavailable, readyzResponse{
				Status: "not_ready",
				Components: map[string]string{
					"config":   componentError,
					"salt":     componentError,
					"pricing":  componentError,
					"database": componentError,
					"writer":   componentError,
				},
			})
			return
		}

		components := map[string]string{
			"config":   statusFromBool(state.configOK),
			"salt":     statusFromBool(state.saltOK),
			"pricing":  statusFromBool(state.pricingOK),
			"database": componentError,
			"writer":   componentError,
		}

		timeout := state.probeTimeout
		if timeout <= 0 {
			timeout = readinessProbeTimeout
		}

		if state.db != nil {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			// Request context/deadline reaches the DB readiness probe.
			if err := state.db.Ready(ctx); err == nil {
				components["database"] = componentOK
			}
		}

		if state.writer != nil && state.writer.Running() {
			components["writer"] = componentOK
		}

		ready := components["config"] == componentOK &&
			components["salt"] == componentOK &&
			components["pricing"] == componentOK &&
			components["database"] == componentOK &&
			components["writer"] == componentOK

		statusCode := http.StatusOK
		status := "ready"
		if !ready {
			statusCode = http.StatusServiceUnavailable
			status = "not_ready"
		}
		// Never include paths, URLs, keys, or internal raw errors.
		writeStableJSON(w, statusCode, readyzResponse{
			Status:     status,
			Components: components,
		})
	})
}

func statusFromBool(ok bool) string {
	if ok {
		return componentOK
	}
	return componentError
}

func writeStableJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(payload)
}
