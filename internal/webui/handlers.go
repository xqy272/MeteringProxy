package webui

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/report"

	"gopkg.in/yaml.v3"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	since, rangeKey, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	result, err := s.reports.Overview(r.Context(), report.OverviewFilter{Since: since, Range: rangeKey})
	if err != nil {
		writeReportQueryFailed(w, "overview", err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
	since, rangeKey, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	keyHash, err := parseKeyHashFilter(r)
	if err != nil {
		writeInvalidKeyHash(w)
		return
	}
	limit, err := parseLimit(r, 20, 100)
	if err != nil {
		writeInvalidFilter(w)
		return
	}

	result, err := s.reports.Issues(r.Context(), report.IssueFilter{
		Since: since, KeyHash: keyHash, Limit: limit, Range: rangeKey,
	})
	if err != nil {
		writeReportQueryFailed(w, "issues", err)
		return
	}
	writeJSON(w, result)
}

// handleGatewayCapabilities answers "what does the proxy see and meter?"
// It merges the static profile.Registry capability matrix with observed
// request_usage traffic so the UI can show usage-metered, request-only,
// passthrough, and missing-usage distinctions. Unknown passthrough is a
// normal capability and is never rendered as an error.
func (s *Server) handleGatewayCapabilities(w http.ResponseWriter, r *http.Request) {
	since, rangeKey, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	result, err := s.reports.GatewayCapabilities(r.Context(), report.GatewayFilter{Since: since, Range: rangeKey})
	if err != nil {
		writeReportQueryFailed(w, "gateway capabilities", err)
		return
	}
	writeJSON(w, result)
}

// ---------- CPA Auth Mirror + Cooldown + Provider Quota (4.3.6) ----------

// handleCPAAuth returns the cached credential health snapshot. It does NOT
// call CPA; use POST /api/cpa/auth/refresh for an explicit refresh.
func (s *Server) handleCPAAuth(w http.ResponseWriter, r *http.Request) {
	rows, lastAt := []db.CredentialHealthRow{}, time.Time{}
	enabled := false
	if s.credPoller != nil {
		enabled = true
		rows, lastAt = s.credPoller.Snapshot()
	}
	writeJSON(w, map[string]any{
		"status":     "ok",
		"enabled":    enabled,
		"checked_at": formatRFC3339OrEmpty(lastAt),
		"items":      rows,
	})
}

// handleCPAAuthRefresh triggers an explicit auth-files fetch. The refresh is
// debounced by the credential poller's MinRefreshInterval and singleflight.
func (s *Server) handleCPAAuthRefresh(w http.ResponseWriter, r *http.Request) {
	queued := false
	if s.credPoller != nil {
		s.credPoller.Refresh()
		queued = true
	}
	writeJSON(w, map[string]any{
		"status":         "ok",
		"refresh_queued": queued,
	})
}

// handleCPACooldownReset calls CPA POST /v0/management/reset-quota. This is a
// maintenance action that clears CPA internal cooldown/routing state. It is
// NOT a provider quota recovery and does not change the provider quota
// snapshot state.
func (s *Server) handleCPACooldownReset(w http.ResponseWriter, r *http.Request) {
	if s.credPoller == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "operation_unavailable", "credential health module is not enabled")
		return
	}
	if err := s.credPoller.ResetCooldown(); err != nil {
		log.Printf("CPA cooldown reset failed: %v", err)
		writeAPIError(w, http.StatusBadGateway, "operation_failed", "failed to reset CPA cooldown")
		return
	}
	writeJSON(w, map[string]any{
		"status":  "ok",
		"message": "CPA cooldown reset requested",
	})
}

// handleProviderQuota returns the cached provider quota snapshot. It does NOT
// call CPA /api-call; use POST /api/provider-quota/refresh for an explicit
// refresh.
func (s *Server) handleProviderQuota(w http.ResponseWriter, r *http.Request) {
	rows, lastAt, apiCallAvail := []db.QuotaCurrentRow{}, time.Time{}, false
	enabled := false
	if s.quotaPoller != nil {
		enabled = true
		rows, lastAt, apiCallAvail = s.quotaPoller.Snapshot()
	}
	moduleStatus := "disabled"
	if enabled {
		if len(rows) == 0 {
			moduleStatus = "not_refreshed"
		} else {
			supported := 0
			for _, q := range rows {
				if q.QuotaSupported > 0 {
					supported++
				}
			}
			if supported == 0 {
				moduleStatus = "unsupported"
			} else {
				moduleStatus = "available"
			}
		}
	}
	writeJSON(w, map[string]any{
		"status":             "ok",
		"enabled":            enabled,
		"module_status":      moduleStatus,
		"api_call_available": apiCallAvail,
		"checked_at":         formatRFC3339OrEmpty(lastAt),
		"items":              rows,
	})
}

// handleProviderQuotaRefresh triggers an explicit provider quota refresh via
// CPA /api-call. The refresh is debounced by MinRefreshInterval and
// singleflight. It returns immediately; the refresh runs asynchronously.
func (s *Server) handleProviderQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	queued := false
	if s.quotaPoller != nil {
		s.quotaPoller.Refresh()
		queued = true
	}
	writeJSON(w, map[string]any{
		"status":         "ok",
		"refresh_queued": queued,
	})
}

// handleProviderQuotaDiagnostics returns recent quota refresh events for
// diagnosing failures, rate limits, and unsupported providers.
func (s *Server) handleProviderQuotaDiagnostics(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r, 50, 100)
	if err != nil {
		writeInvalidFilter(w)
		return
	}
	events, eventsStatus, eventsPartial, err := s.recentQuotaDiagnostics(r.Context(), limit)
	if err != nil {
		writeReportQueryFailed(w, "provider quota diagnostics", err)
		return
	}
	writeJSON(w, map[string]any{
		"status":        "ok",
		"enabled":       s.quotaPoller != nil,
		"events":        events,
		"events_status": eventsStatus,
		"partial":       eventsPartial,
	})
}

// handleModelAssets returns a per-model view merging request_usage traffic with
// pricing configuration. It helps discover used-but-unpriced models and
// request-only models that should not be read as complete cost (4.5 节).
func (s *Server) handleModelAssets(w http.ResponseWriter, r *http.Request) {
	since, rangeKey, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	result, err := s.reports.ModelAssets(r.Context(), report.ModelAssetsFilter{Since: since, Range: rangeKey})
	if err != nil {
		writeReportQueryFailed(w, "model assets", err)
		return
	}
	writeJSON(w, result)
}

// handlePricingStub generates a YAML pricing stub with all-zero prices for
// models that have been used in the time range but are not yet configured in
// pricing.yaml. It does not guess prices; the operator fills in real values.
func (s *Server) handlePricingStub(w http.ResponseWriter, r *http.Request) {
	since, rangeKey, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	assets, err := s.reports.ModelAssets(r.Context(), report.ModelAssetsFilter{Since: since, Range: rangeKey})
	if err != nil {
		writeReportQueryFailed(w, "pricing stub", err)
		return
	}

	type stubModel struct {
		InputPer1M         float64 `yaml:"input_per_1m"`
		CachedInputPer1M   float64 `yaml:"cached_input_per_1m"`
		OutputPer1M        float64 `yaml:"output_per_1m"`
		ReasoningPer1M     float64 `yaml:"reasoning_per_1m"`
		CacheCreationPer1M float64 `yaml:"cache_creation_per_1m"`
	}
	stub := struct {
		Pricing map[string]stubModel `yaml:"pricing"`
	}{
		Pricing: make(map[string]stubModel),
	}
	var unpricedCount int
	for _, item := range assets.Items {
		if item.Model == "" || item.Model == "unidentified" {
			continue
		}
		if item.PricingSource != "unpriced" {
			continue
		}
		stub.Pricing[item.Model] = stubModel{}
		unpricedCount++
	}

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("X-Unpriced-Model-Count", strconv.Itoa(unpricedCount))
	if unpricedCount == 0 {
		if _, err := w.Write([]byte("# All used models in this range already have pricing configured.\n")); err != nil {
			log.Printf("pricing stub write failed: %v", err)
		}
		return
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(stub); err != nil {
		log.Printf("pricing stub encode failed: %v", err)
	}
	if err := enc.Close(); err != nil {
		log.Printf("pricing stub close failed: %v", err)
	}
}
