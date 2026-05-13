package webui

import (
	"embed"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/pricing"
	"ai-gateway-metering-proxy/internal/profile"
	"ai-gateway-metering-proxy/internal/store"
	"ai-gateway-metering-proxy/internal/writer"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	db        store.ReportStore
	pricing   *pricing.Pricing
	writer    *writer.BatchWriter
	registry  *profile.Registry
	basePath  string
	apiPrefix string
	mux       *http.ServeMux

	staticFS        fs.FS
	meteringEnabled func() bool

	credPoller interface {
		Snapshot() ([]db.CredentialHealthRow, time.Time)
		Refresh()
	}
	quotaPoller interface {
		Snapshot() ([]db.QuotaCurrentRow, time.Time, bool)
		APICallAvailable() bool
		Refresh()
	}
	usageQueuePoller interface {
		Snapshot() (bool, time.Time, string)
	}
	correlationMode string
}

// New creates a Server that serves static files from the embedded filesystem.
func New(database store.ReportStore, pricingData *pricing.Pricing, batchWriter *writer.BatchWriter, registry *profile.Registry, basePath string) *Server {
	staticFS, _ := fs.Sub(staticFiles, "static")
	return newServer(database, pricingData, batchWriter, registry, basePath, staticFS)
}

// NewWithStaticFS creates a Server that serves static files from the given
// filesystem. Use this for local development (os.DirFS on the static/ directory)
// or testing with custom asset sets.
func NewWithStaticFS(database store.ReportStore, pricingData *pricing.Pricing, batchWriter *writer.BatchWriter, registry *profile.Registry, basePath string, staticFS fs.FS) *Server {
	return newServer(database, pricingData, batchWriter, registry, basePath, staticFS)
}

func newServer(database store.ReportStore, pricingData *pricing.Pricing, batchWriter *writer.BatchWriter, registry *profile.Registry, basePath string, staticFS fs.FS) *Server {
	basePath = strings.TrimRight(basePath, "/")
	apiPrefix := basePath + "/api/"

	s := &Server{
		db:              database,
		pricing:         pricingData,
		writer:          batchWriter,
		registry:        registry,
		basePath:        basePath,
		apiPrefix:       apiPrefix,
		staticFS:        staticFS,
		meteringEnabled: func() bool { return true },
		mux:             http.NewServeMux(),
	}

	s.mux.HandleFunc(basePath, s.handleIndex)

	fileServer := http.StripPrefix(basePath, http.FileServer(http.FS(staticFS)))

	subtreePattern := basePath + "/"
	s.mux.HandleFunc(subtreePattern, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, apiPrefix) {
			s.routeAPI(w, r)
			return
		}
		if path == subtreePattern || path == basePath {
			s.handleIndex(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})

	return s
}

// SetMeteringEnabledFunc sets the callback used to report metering state.
func (s *Server) SetMeteringEnabledFunc(fn func() bool) {
	s.meteringEnabled = fn
}

func (s *Server) SetCredPoller(p interface {
	Snapshot() ([]db.CredentialHealthRow, time.Time)
	Refresh()
}) {
	s.credPoller = p
}

func (s *Server) SetQuotaPoller(p interface {
	Snapshot() ([]db.QuotaCurrentRow, time.Time, bool)
	APICallAvailable() bool
	Refresh()
}) {
	s.quotaPoller = p
}

func (s *Server) SetUsageQueuePoller(p interface {
	Snapshot() (bool, time.Time, string)
}) {
	s.usageQueuePoller = p
}

func (s *Server) SetCorrelationMode(mode string) {
	s.correlationMode = mode
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routeAPI(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/api/overview"):
		s.handleOverview(w, r)
	case strings.HasSuffix(path, "/api/issues"):
		s.handleIssues(w, r)
	case strings.HasSuffix(path, "/api/summary"):
		s.handleSummary(w, r)
	case strings.HasSuffix(path, "/api/timeseries"):
		s.handleTimeseries(w, r)
	case strings.HasSuffix(path, "/api/activity"):
		s.handleActivity(w, r)
	case strings.HasSuffix(path, "/api/models"):
		s.handleModels(w, r)
	case strings.HasSuffix(path, "/api/keys"):
		s.handleKeys(w, r)
	case strings.HasSuffix(path, "/api/requests"):
		s.handleRequests(w, r)
	case strings.HasSuffix(path, "/api/errors"):
		s.handleErrors(w, r)
	case strings.HasSuffix(path, "/api/health"):
		s.handleHealth(w, r)
	case strings.HasSuffix(path, "/api/metadata"):
		s.handleMetadata(w, r)
	case strings.HasSuffix(path, "/api/quota"):
		s.handleQuota(w, r)
	case strings.HasSuffix(path, "/api/quota/refresh"):
		s.handleQuotaRefresh(w, r)
	case strings.HasSuffix(path, "/api/observability"):
		s.handleObservability(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	htmlBytes, err := fs.ReadFile(s.staticFS, "index.html")
	if err != nil {
		http.Error(w, "index not found", 500)
		return
	}
	apiBase := html.EscapeString(s.basePath + "/api/")
	staticBase := html.EscapeString(s.basePath)
	injected := strings.ReplaceAll(string(htmlBytes), "__METERING_API_BASE__", apiBase)
	injected = strings.ReplaceAll(injected, "__METERING_BASE__", staticBase)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(injected))
}

func parseRange(r *http.Request) (time.Time, string) {
	q := r.URL.Query()
	rangeStr := q.Get("range")
	switch rangeStr {
	case "7d":
		return time.Now().Add(-7 * 24 * time.Hour), rangeStr
	case "30d":
		return time.Now().Add(-30 * 24 * time.Hour), rangeStr
	case "today":
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), rangeStr
	default:
		return time.Now().Add(-24 * time.Hour), "24h"
	}
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func setNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	row, err := s.db.Summary(since)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	models, _ := s.db.Models(since)
	var totalCost float64
	for _, m := range models {
		cost, _ := s.pricing.CostWithCacheCreation(m.Model, m.InputTokens, m.OutputTokens, m.ReasoningTokens, m.CachedTokens, m.CacheCreationTokens)
		totalCost += cost
	}
	report := event.SummaryFromDB(row)
	report.TotalCost = totalCost
	writeJSON(w, report)
}

func (s *Server) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	bucketStr := r.URL.Query().Get("bucket")
	bucketMin := 60
	switch bucketStr {
	case "1h":
		bucketMin = 60
	case "1d":
		bucketMin = 1440
	}
	rows, err := s.db.Timeseries(since, bucketMin)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	report := event.TimeseriesFromDB(rows)
	if report == nil {
		report = []event.TimeseriesReport{}
	}
	modelRows, err := s.db.ModelTimeseries(since, bucketMin)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type bucketCost struct {
		cost           float64
		unpricedModels int64
	}
	costs := make(map[string]*bucketCost)
	for _, row := range modelRows {
		acc := costs[row.Timestamp]
		if acc == nil {
			acc = &bucketCost{}
			costs[row.Timestamp] = acc
		}
		cost, known := s.pricing.CostWithCacheCreation(row.Model, row.InputTokens, row.OutputTokens, row.ReasoningTokens, row.CachedTokens, row.CacheCreationTokens)
		if known {
			acc.cost += cost
		} else {
			acc.unpricedModels++
		}
	}
	for i := range report {
		if acc := costs[report[i].Timestamp]; acc != nil {
			report[i].Cost = acc.cost
			report[i].CostKnown = acc.unpricedModels == 0
			report[i].UnpricedModels = acc.unpricedModels
		}
	}
	writeJSON(w, report)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	row, err := s.db.Activity(since)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, event.ActivityFromDB(row))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	rows, err := s.db.Models(since)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	report := event.ModelsFromDB(rows)
	if report == nil {
		report = []event.ModelReport{}
	}
	for i := range report {
		cost, known := s.pricing.CostWithCacheCreation(report[i].Model, report[i].InputTokens, report[i].OutputTokens, report[i].ReasoningTokens, report[i].CachedTokens, report[i].CacheCreationTokens)
		report[i].Cost = cost
		report[i].CostKnown = known
		if rows[i].MissingUsageCount > 0 || len(rows[i].ModelReturnedSourceCounts) > 0 || len(rows[i].UsageSourceCounts) > 0 {
			report[i].MissingUsageCount = rows[i].MissingUsageCount
		}
	}
	for i := range report {
		srcCounts, usageCounts, err := s.db.ModelSourceCounts(since, report[i].Model)
		if err == nil && (len(srcCounts) > 0 || len(usageCounts) > 0) {
			report[i].ModelReturnedSourceCounts = srcCounts
			report[i].UsageSourceCounts = usageCounts
		}
	}
	writeJSON(w, report)
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	rows, err := s.db.Keys(since)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	report := event.KeysFromDB(rows)
	if report == nil {
		report = []event.KeyReport{}
	}
	writeJSON(w, report)
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var statusMin, statusMax int
	switch r.URL.Query().Get("status") {
	case "success":
		statusMin = 200
		statusMax = 300
	case "4xx":
		statusMin = 400
		statusMax = 500
	case "5xx":
		statusMin = 500
	default:
	}
	model := r.URL.Query().Get("model")
	endpoint := r.URL.Query().Get("endpoint")
	errorClass := r.URL.Query().Get("error_class")
	since, _ := parseRange(r)

	rows, err := s.db.Requests(limit, statusMin, statusMax, model, endpoint, errorClass, since)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	report := event.RequestsFromDB(rows)
	if report == nil {
		report = []event.RequestReport{}
	}
	writeJSON(w, report)
}

func (s *Server) handleErrors(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)

	type errorResp struct {
		Timeline           []event.ErrorTimelineReport `json:"timeline"`
		Source             string                      `json:"source"`
		BucketCount        int                         `json:"bucket_count"`
		NonzeroBucketCount int                         `json:"nonzero_bucket_count"`
		QueueDepth         int64                       `json:"queue_depth"`
		ParseErrors        int64                       `json:"parse_errors"`
		DBErrors           int64                       `json:"db_errors"`
		DroppedEvents      int64                       `json:"dropped_events"`
	}

	writeErrResp := func(source string, timeline []event.ErrorTimelineReport) {
		latestHealth, _ := s.db.LatestHealth()
		bucketCount := len(timeline)
		nonzeroTimeline := filterNonzeroErrors(timeline)
		nonzeroBucketCount := len(nonzeroTimeline)
		if r.URL.Query().Get("nonzero") == "true" {
			timeline = nonzeroTimeline
		}
		resp := errorResp{
			Timeline:           timeline,
			Source:             source,
			BucketCount:        bucketCount,
			NonzeroBucketCount: nonzeroBucketCount,
		}
		if latestHealth != nil {
			resp.QueueDepth = latestHealth.QueueDepth
			resp.ParseErrors = latestHealth.ParseErrors
			resp.DBErrors = latestHealth.DBErrors
			resp.DroppedEvents = latestHealth.DroppedEvents
		}
		if resp.Timeline == nil {
			resp.Timeline = []event.ErrorTimelineReport{}
		}
		writeJSON(w, resp)
	}

	var sources []string
	var timelines [][]event.ErrorTimelineReport
	healthRows, err := s.db.ErrorTimeline(since)
	if err == nil && len(healthRows) > 0 {
		sources = append(sources, "health_metrics")
		timelines = append(timelines, event.ErrorTimelineFromDB(healthRows))
	}

	reqErrorRows, err := s.db.ErrorTimelineFromRequests(since)
	if err == nil && len(reqErrorRows) > 0 {
		sources = append(sources, "request_usage")
		timelines = append(timelines, event.ErrorTimelineFromDB(reqErrorRows))
	}

	source := "request_usage"
	if len(sources) > 0 {
		source = strings.Join(sources, "+")
	}
	writeErrResp(source, combineErrorTimelines(timelines...))
}

func filterNonzeroErrors(rows []event.ErrorTimelineReport) []event.ErrorTimelineReport {
	if len(rows) == 0 {
		return []event.ErrorTimelineReport{}
	}
	filtered := make([]event.ErrorTimelineReport, 0, len(rows))
	for _, r := range rows {
		if r.Count+r.ParseErrors+r.DBErrors+r.DroppedEvents > 0 {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func combineErrorTimelines(groups ...[]event.ErrorTimelineReport) []event.ErrorTimelineReport {
	byTimestamp := map[string]*event.ErrorTimelineReport{}
	for _, rows := range groups {
		for _, row := range rows {
			key := row.Timestamp
			if key == "" {
				key = "unknown"
			}
			existing := byTimestamp[key]
			if existing == nil {
				copy := row
				byTimestamp[key] = &copy
				continue
			}
			existing.Count += row.Count
			existing.ParseErrors += row.ParseErrors
			existing.DBErrors += row.DBErrors
			existing.DroppedEvents += row.DroppedEvents
		}
	}

	combined := make([]event.ErrorTimelineReport, 0, len(byTimestamp))
	for _, row := range byTimestamp {
		combined = append(combined, *row)
	}
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Timestamp < combined[j].Timestamp
	})
	return combined
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	queueDepth, dropped, parseErrors, dbErrors := s.writer.Snapshot()
	latestHealth, _ := s.db.LatestHealth()
	meteringEnabled := s.meteringEnabled()
	resp := map[string]any{
		"queue_depth":      queueDepth,
		"dropped_events":   dropped,
		"parse_errors":     parseErrors,
		"db_write_errors":  dbErrors,
		"latest_health":    event.HealthFromDB(latestHealth),
		"metering_enabled": meteringEnabled,
		"capture_disabled": !meteringEnabled,
	}
	writeJSON(w, resp)
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	meta := event.MetadataReport{
		Ranges: []event.RangeMeta{
			{Key: "24h", Label: "Last 24 Hours", Bucket: "1h"},
			{Key: "today", Label: "Today", Bucket: "1h"},
			{Key: "7d", Label: "Last 7 Days", Bucket: "1h"},
			{Key: "30d", Label: "Last 30 Days", Bucket: "1d"},
		},
		Buckets: []event.BucketMeta{
			{Key: "1h", Label: "1 Hour"},
			{Key: "1d", Label: "1 Day"},
		},
		MeteringKinds: []string{
			event.MeteringLLMTokens,
			event.MeteringNone,
		},
		CaptureModes: []string{
			event.CaptureUsageMetered,
			event.CapturePassthrough,
			event.CaptureRequestOnly,
		},
	}

	if s.registry != nil {
		for _, p := range s.registry.Profiles() {
			meta.Endpoints = append(meta.Endpoints, p.ToEndpointMeta())
		}
	}

	writeJSON(w, meta)
}

func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	credRows, credTime := []db.CredentialHealthRow{}, time.Time{}
	if s.credPoller != nil {
		credRows, credTime = s.credPoller.Snapshot()
	}

	quotaRows, quotaTime, apiCallAvail := []db.QuotaCurrentRow{}, time.Time{}, false
	if s.quotaPoller != nil {
		quotaRows, quotaTime, apiCallAvail = s.quotaPoller.Snapshot()
	}

	if len(quotaRows) > 0 || (s.quotaPoller != nil && apiCallAvail) {
		type providerSummary struct {
			Provider         string `json:"provider"`
			CredentialCount  int    `json:"credential_count"`
			LowCount         int    `json:"low_count"`
			ExhaustedCount   int    `json:"exhausted_count"`
			ErrorCount       int    `json:"error_count"`
			UnsupportedCount int    `json:"unsupported_count"`
			UnknownCount     int    `json:"unknown_count"`
		}
		summaryMap := map[string]*providerSummary{}
		for _, q := range quotaRows {
			ps, ok := summaryMap[q.Provider]
			if !ok {
				ps = &providerSummary{Provider: q.Provider}
				summaryMap[q.Provider] = ps
			}
			ps.CredentialCount++
			switch q.Status {
			case "low":
				ps.LowCount++
			case "exhausted":
				ps.ExhaustedCount++
			case "error":
				ps.ErrorCount++
			case "unknown":
				ps.UnknownCount++
			case "unsupported":
				ps.UnsupportedCount++
			}
		}

		phase := "quota_snapshot"
		if len(quotaRows) == 0 {
			phase = "credential_health"
		}

		providers := make([]providerSummary, 0, len(summaryMap))
		for _, ps := range summaryMap {
			providers = append(providers, *ps)
		}

		checkedAt := quotaTime
		if checkedAt.IsZero() {
			checkedAt = credTime
		}

		resp := map[string]any{
			"status":               "ok",
			"phase":                phase,
			"full_quota_available": apiCallAvail,
			"module_status":        "available",
			"stale":                false,
			"checked_at":           checkedAt.Format(time.RFC3339),
			"providers":            providers,
			"items":                quotaRows,
		}
		writeJSON(w, resp)
		return
	}

	if len(credRows) > 0 {
		type providerSummary struct {
			Provider         string `json:"provider"`
			CredentialCount  int    `json:"credential_count"`
			ReadyCount       int    `json:"ready_count"`
			UnavailableCount int    `json:"unavailable_count"`
			DisabledCount    int    `json:"disabled_count"`
		}
		summaryMap := map[string]*providerSummary{}
		for _, c := range credRows {
			ps, ok := summaryMap[c.Provider]
			if !ok {
				ps = &providerSummary{Provider: c.Provider}
				summaryMap[c.Provider] = ps
			}
			ps.CredentialCount++
			switch c.Status {
			case "ready":
				ps.ReadyCount++
			case "unavailable":
				ps.UnavailableCount++
			case "disabled":
				ps.DisabledCount++
			}
		}

		providers := make([]providerSummary, 0, len(summaryMap))
		for _, ps := range summaryMap {
			providers = append(providers, *ps)
		}

		resp := map[string]any{
			"status":               "ok",
			"phase":                "credential_health",
			"full_quota_available": false,
			"module_status":        "partial",
			"checked_at":           credTime.Format(time.RFC3339),
			"providers":            providers,
			"items":                credRows,
		}
		writeJSON(w, resp)
		return
	}

	writeJSON(w, map[string]any{
		"status":               "ok",
		"phase":                "credential_health",
		"full_quota_available": false,
		"module_status":        "disabled",
		"checked_at":           "",
		"providers":            []any{},
		"items":                []any{},
	})
}

func (s *Server) handleQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	queued := false
	if s.quotaPoller != nil {
		s.quotaPoller.Refresh()
		queued = true
	}
	if s.credPoller != nil {
		s.credPoller.Refresh()
		queued = true
	}
	writeJSON(w, map[string]any{
		"status":         "ok",
		"refresh_queued": queued,
	})
}

func (s *Server) handleObservability(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"request_capture": map[string]any{
			"parse_errors":   0,
			"dropped_events": 0,
		},
		"side_channel": map[string]any{
			"enabled":       s.usageQueuePoller != nil,
			"connected":     false,
			"merge_mode":    s.correlationMode,
			"last_event_at": "",
			"last_error":    "",
		},
		"credential_health": map[string]any{
			"enabled": s.credPoller != nil,
		},
		"quota": map[string]any{
			"enabled": s.quotaPoller != nil,
		},
	}

	if s.usageQueuePoller != nil {
		connected, lastAt, lastErr := s.usageQueuePoller.Snapshot()
		counts, _ := s.db.SideUsageStatusCounts(time.Now().Add(-1 * time.Hour))
		resp["side_channel"] = map[string]any{
			"enabled":          true,
			"connected":        connected,
			"merge_mode":       s.correlationMode,
			"last_event_at":    lastAt.Format(time.RFC3339),
			"last_received_at": lastAt.Format(time.RFC3339),
			"last_error":       lastErr,
			"matched_1h":       counts["matched"],
			"unmatched_1h":     counts["unmatched"],
			"stored_only_1h":   counts["stored_only"],
			"conflicts_1h":     counts["conflict"],
		}
	}

	if s.writer != nil {
		_, dropped, parseErrors, _ := s.writer.Snapshot()
		resp["request_capture"] = map[string]any{
			"parse_errors":   parseErrors,
			"dropped_events": dropped,
		}
	}

	captured1h, skipped1h, failed1h, _ := s.db.CaptureOutcomeCounts(time.Now().Add(-1 * time.Hour))
	resp["request_capture"] = map[string]any{
		"parse_errors":       getMapVal(resp["request_capture"], "parse_errors"),
		"dropped_events":     getMapVal(resp["request_capture"], "dropped_events"),
		"captured_1h":        captured1h,
		"skipped_1h":         skipped1h,
		"failed_1h":          failed1h,
		"capture_skipped_1h": skipped1h,
		"capture_failed_1h":  failed1h,
	}

	if s.credPoller != nil {
		credRows, credTime := s.credPoller.Snapshot()
		unavailableCount := 0
		errorCount := 0
		for _, c := range credRows {
			if c.Status == "unavailable" {
				unavailableCount++
			}
			if c.Status == "error" {
				errorCount++
			}
		}
		resp["credential_health"] = map[string]any{
			"enabled":           true,
			"last_checked_at":   credTime.Format(time.RFC3339),
			"unavailable_count": unavailableCount,
			"error_count":       errorCount,
		}
	}

	if s.quotaPoller != nil {
		quotaRows, quotaTime, apiCallAvail := s.quotaPoller.Snapshot()
		phase := "credential_health"
		if len(quotaRows) > 0 || apiCallAvail {
			phase = "quota_snapshot"
		}
		staleCount := 0
		errorCount := 0
		for _, q := range quotaRows {
			if q.Status == "stale" {
				staleCount++
			}
			if q.Status == "error" {
				errorCount++
			}
		}
		resp["quota"] = map[string]any{
			"enabled":              true,
			"phase":                phase,
			"full_quota_available": apiCallAvail,
			"last_checked_at":      quotaTime.Format(time.RFC3339),
			"stale_count":          staleCount,
			"error_count":          errorCount,
		}
	}

	writeJSON(w, resp)
}

func getMapVal(m any, key string) any {
	if mm, ok := m.(map[string]any); ok {
		return mm[key]
	}
	return nil
}
