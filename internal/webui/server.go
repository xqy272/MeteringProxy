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
	"ai-gateway-metering-proxy/internal/profile"
	"ai-gateway-metering-proxy/internal/report"
	"ai-gateway-metering-proxy/internal/store"
	"ai-gateway-metering-proxy/internal/writer"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	db        store.ReportStore
	reports   report.CoreReporter
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
		ResetCooldown() error
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
// models is required and must be wired by the composition root.
func New(database store.ReportStore, reports report.CoreReporter, batchWriter *writer.BatchWriter, registry *profile.Registry, basePath string) *Server {
	staticFS, _ := fs.Sub(staticFiles, "static")
	return newServer(database, reports, batchWriter, registry, basePath, staticFS)
}

// NewWithStaticFS creates a Server that serves static files from the given
// filesystem. Use this for local development (os.DirFS on the static/ directory)
// or testing with custom asset sets.
func NewWithStaticFS(database store.ReportStore, reports report.CoreReporter, batchWriter *writer.BatchWriter, registry *profile.Registry, basePath string, staticFS fs.FS) *Server {
	return newServer(database, reports, batchWriter, registry, basePath, staticFS)
}

func newServer(database store.ReportStore, reports report.CoreReporter, batchWriter *writer.BatchWriter, registry *profile.Registry, basePath string, staticFS fs.FS) *Server {
	if reports == nil {
		panic("webui: core reporter is required")
	}
	basePath = strings.TrimRight(basePath, "/")
	apiPrefix := basePath + "/api/"

	s := &Server{
		db:              database,
		reports:         reports,
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
	ResetCooldown() error
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
	case strings.HasSuffix(path, "/api/multimodal/summary"):
		s.handleMultimodalSummary(w, r)
	case strings.HasSuffix(path, "/api/images/summary"):
		s.handleImageSummary(w, r)
	case strings.HasSuffix(path, "/api/images/models"):
		s.handleImageModels(w, r)
	case strings.HasSuffix(path, "/api/images/requests"):
		s.handleImageRequests(w, r)
	case strings.HasSuffix(path, "/api/errors"):
		s.handleErrors(w, r)
	case strings.HasSuffix(path, "/api/health"):
		s.handleHealth(w, r)
	case strings.HasSuffix(path, "/api/metadata"):
		s.handleMetadata(w, r)
	case strings.HasSuffix(path, "/api/quota/diagnostics"):
		s.handleQuotaDiagnostics(w, r)
	case strings.HasSuffix(path, "/api/quota"):
		s.handleQuota(w, r)
	case strings.HasSuffix(path, "/api/quota/refresh"):
		s.handleQuotaRefresh(w, r)
	case strings.HasSuffix(path, "/api/observability"):
		s.handleObservability(w, r)
	case strings.HasSuffix(path, "/api/gateway/capabilities"):
		s.handleGatewayCapabilities(w, r)
	case strings.HasSuffix(path, "/api/model-assets"):
		s.handleModelAssets(w, r)
	case strings.HasSuffix(path, "/api/pricing/stub"):
		s.handlePricingStub(w, r)
	case strings.HasSuffix(path, "/api/cpa/auth"):
		s.handleCPAAuth(w, r)
	case strings.HasSuffix(path, "/api/cpa/auth/refresh"):
		s.handleCPAAuthRefresh(w, r)
	case strings.HasSuffix(path, "/api/cpa/cooldown/reset"):
		s.handleCPACooldownReset(w, r)
	case strings.HasSuffix(path, "/api/provider-quota"):
		s.handleProviderQuota(w, r)
	case strings.HasSuffix(path, "/api/provider-quota/refresh"):
		s.handleProviderQuotaRefresh(w, r)
	case strings.HasSuffix(path, "/api/provider-quota/diagnostics"):
		s.handleProviderQuotaDiagnostics(w, r)
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

func parseKeyHashFilter(r *http.Request) (string, error) {
	value := r.URL.Query().Get("key_hash")
	if err := report.ValidateKeyHashFilter(value); err != nil {
		return "", err
	}
	return value, nil
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func formatRFC3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func setNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	result, err := s.reports.Summary(r.Context(), report.SummaryFilter{Since: since})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	keyHash, err := parseKeyHashFilter(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	bucketStr := r.URL.Query().Get("bucket")
	bucketMin := 60
	switch bucketStr {
	case "1h":
		bucketMin = 60
	case "1d":
		bucketMin = 1440
	}
	rows, err := s.reports.Timeseries(r.Context(), report.TimeseriesFilter{Since: since, BucketMin: bucketMin, KeyHash: keyHash})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if rows == nil {
		rows = []report.TimeseriesReport{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	keyHash, err := parseKeyHashFilter(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	row, err := s.reports.Activity(r.Context(), report.ActivityFilter{Since: since, KeyHash: keyHash})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, row)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	keyHash, err := parseKeyHashFilter(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	models, err := s.reports.Models(r.Context(), report.ModelsFilter{Since: since, KeyHash: keyHash})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if models == nil {
		models = []report.ModelReport{}
	}
	writeJSON(w, models)
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	rows, err := s.reports.Keys(r.Context(), report.KeysFilter{Since: since})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if rows == nil {
		rows = []report.KeyReport{}
	}
	writeJSON(w, rows)
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
	keyHash, err := parseKeyHashFilter(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rows, err := s.reports.Requests(r.Context(), report.RequestFilter{
		Since: since, KeyHash: keyHash, Limit: limit,
		StatusMin: statusMin, StatusMax: statusMax,
		Model: model, Endpoint: endpoint, ErrorClass: errorClass,
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if rows == nil {
		rows = []report.RequestReport{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleMultimodalSummary(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	rows, err := s.db.MultimodalSummary(since)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if rows == nil {
		rows = []db.MultimodalSummaryRow{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleImageSummary(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	result, err := s.reports.ImageSummary(r.Context(), report.ImagesFilter{Since: since})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleImageModels(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	rows, err := s.reports.ImageModels(r.Context(), report.ImagesFilter{Since: since})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if rows == nil {
		rows = []report.ImageModelReport{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleImageRequests(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	since, _ := parseRange(r)
	rows, err := s.db.ImageRequests(limit, since)
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
			event.MeteringImageTokens,
			event.MeteringEmbeddingTokens,
			event.MeteringAudioSeconds,
			event.MeteringRequestOnly,
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
	diagnostics := []db.QuotaRefreshEventRow{}
	if s.quotaPoller != nil {
		diagnostics = s.recentQuotaDiagnostics(12)
	}
	credRows, credTime := []db.CredentialHealthRow{}, time.Time{}
	if s.credPoller != nil {
		credRows, credTime = s.credPoller.Snapshot()
	}

	quotaRows, quotaTime, apiCallAvail := []db.QuotaCurrentRow{}, time.Time{}, false
	if s.quotaPoller != nil {
		quotaRows, quotaTime, apiCallAvail = s.quotaPoller.Snapshot()
	}

	supportedQuotaRows := 0
	unsupportedQuotaRows := 0
	for _, q := range quotaRows {
		if q.QuotaSupported > 0 {
			supportedQuotaRows++
		}
		if q.Status == "unsupported" {
			unsupportedQuotaRows++
		}
	}

	if supportedQuotaRows > 0 || (len(credRows) == 0 && (len(quotaRows) > 0 || s.quotaPoller != nil && apiCallAvail || s.quotaPoller != nil)) {
		type providerSummary struct {
			Provider         string `json:"provider"`
			CredentialCount  int    `json:"credential_count"`
			OKCount          int    `json:"ok_count"`
			WarningCount     int    `json:"warning_count"`
			LowCount         int    `json:"low_count"`
			ExhaustedCount   int    `json:"exhausted_count"`
			ErrorCount       int    `json:"error_count"`
			StaleCount       int    `json:"stale_count"`
			UnsupportedCount int    `json:"unsupported_count"`
			UnknownCount     int    `json:"unknown_count"`
		}
		summaryMap := map[string]*providerSummary{}
		supportedRows := 0
		unhealthyRows := 0
		for _, q := range quotaRows {
			ps, ok := summaryMap[q.Provider]
			if !ok {
				ps = &providerSummary{Provider: q.Provider}
				summaryMap[q.Provider] = ps
			}
			ps.CredentialCount++
			if q.QuotaSupported > 0 {
				supportedRows++
			}
			switch q.Status {
			case "ok":
				ps.OKCount++
			case "warning":
				ps.WarningCount++
				unhealthyRows++
			case "low":
				ps.LowCount++
				unhealthyRows++
			case "exhausted":
				ps.ExhaustedCount++
				unhealthyRows++
			case "error":
				ps.ErrorCount++
				unhealthyRows++
			case "stale":
				ps.StaleCount++
				unhealthyRows++
			case "unknown":
				ps.UnknownCount++
			case "unsupported":
				ps.UnsupportedCount++
				unhealthyRows++
			}
		}

		phase := "quota_snapshot"
		if len(quotaRows) == 0 || supportedRows == 0 {
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
		moduleStatus := "unavailable"
		if len(quotaRows) > 0 {
			moduleStatus = "partial"
			if supportedRows == 0 && unsupportedQuotaRows > 0 && apiCallAvail {
				moduleStatus = "unsupported"
			}
			if apiCallAvail && supportedRows > 0 && unhealthyRows == 0 {
				moduleStatus = "available"
			}
		} else if apiCallAvail {
			moduleStatus = "partial"
		}

		resp := map[string]any{
			"status":               "ok",
			"phase":                phase,
			"full_quota_available": apiCallAvail && supportedRows > 0,
			"module_status":        moduleStatus,
			"stale":                unhealthyRows > 0,
			"checked_at":           formatRFC3339OrEmpty(checkedAt),
			"providers":            providers,
			"items":                quotaRows,
			"credential_items":     credRows,
			"diagnostics":          diagnostics,
		}
		writeJSON(w, resp)
		return
	}

	if len(credRows) > 0 {
		type providerSummary struct {
			Provider         string `json:"provider"`
			CredentialCount  int    `json:"credential_count"`
			ReadyCount       int    `json:"ready_count"`
			WarningCount     int    `json:"warning_count"`
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
			case "warning":
				ps.WarningCount++
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

		moduleStatus := "partial"
		if s.quotaPoller != nil {
			if apiCallAvail && unsupportedQuotaRows > 0 && supportedQuotaRows == 0 {
				moduleStatus = "unsupported"
			}
		}
		resp := map[string]any{
			"status":               "ok",
			"phase":                "credential_health",
			"full_quota_available": false,
			"module_status":        moduleStatus,
			"checked_at":           formatRFC3339OrEmpty(credTime),
			"providers":            providers,
			"items":                credRows,
			"quota_items":          quotaRows,
			"diagnostics":          diagnostics,
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
		"diagnostics":          diagnostics,
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

func (s *Server) handleQuotaDiagnostics(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	credRows := []db.CredentialHealthRow{}
	credTime := time.Time{}
	if s.credPoller != nil {
		credRows, credTime = s.credPoller.Snapshot()
	}
	quotaRows := []db.QuotaCurrentRow{}
	quotaTime := time.Time{}
	apiCallAvailable := false
	if s.quotaPoller != nil {
		quotaRows, quotaTime, apiCallAvailable = s.quotaPoller.Snapshot()
	}
	events := s.recentQuotaDiagnostics(limit)
	checkedAt := quotaTime
	if checkedAt.IsZero() {
		checkedAt = credTime
	}
	writeJSON(w, map[string]any{
		"status":                    "ok",
		"enabled":                   s.credPoller != nil || s.quotaPoller != nil,
		"credential_health_enabled": s.credPoller != nil,
		"quota_enabled":             s.quotaPoller != nil,
		"api_call_available":        apiCallAvailable,
		"checked_at":                formatRFC3339OrEmpty(checkedAt),
		"credentials":               credentialDiagnosticsSummary(credRows),
		"quota":                     quotaDiagnosticsSummary(quotaRows),
		"events":                    events,
	})
}

func (s *Server) handleObservability(w http.ResponseWriter, r *http.Request) {
	credRows := []db.CredentialHealthRow{}
	credTime := time.Time{}
	quotaDiagnostics := []db.QuotaRefreshEventRow{}
	if s.quotaPoller != nil {
		quotaDiagnostics = s.recentQuotaDiagnostics(6)
	}

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
			"last_event_at":    formatRFC3339OrEmpty(lastAt),
			"last_received_at": formatRFC3339OrEmpty(lastAt),
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
		credRows, credTime = s.credPoller.Snapshot()
		unavailableCount := 0
		warningCount := 0
		errorCount := 0
		for _, c := range credRows {
			if c.Status == "unavailable" {
				unavailableCount++
			}
			if c.Status == "warning" {
				warningCount++
			}
			if c.Status == "error" {
				errorCount++
			}
		}
		resp["credential_health"] = map[string]any{
			"enabled":           true,
			"last_checked_at":   formatRFC3339OrEmpty(credTime),
			"warning_count":     warningCount,
			"unavailable_count": unavailableCount,
			"error_count":       errorCount,
		}
	}

	if s.quotaPoller != nil {
		quotaRows, quotaTime, apiCallAvail := s.quotaPoller.Snapshot()
		phase := "credential_health"
		supportedRows := 0
		unhealthyRows := 0
		if len(quotaRows) > 0 {
			phase = "quota_snapshot"
		}
		staleCount := 0
		errorCount := 0
		unsupportedCount := 0
		for _, q := range quotaRows {
			if q.QuotaSupported > 0 {
				supportedRows++
			}
			if q.Status == "stale" {
				staleCount++
			}
			if q.Status == "error" || q.Status == "unsupported" {
				errorCount++
			}
			if q.Status == "unsupported" {
				unsupportedCount++
			}
			switch q.Status {
			case "warning", "low", "exhausted", "error", "stale", "unsupported":
				unhealthyRows++
			}
		}
		if supportedRows == 0 {
			phase = "credential_health"
		}
		credentialFallback := phase == "credential_health" && len(quotaRows) == 0 && len(credRows) > 0
		moduleStatus := "unavailable"
		if len(quotaRows) > 0 {
			moduleStatus = "partial"
			if apiCallAvail && supportedRows == 0 && unsupportedCount > 0 {
				moduleStatus = "unsupported"
			}
			if apiCallAvail && supportedRows > 0 && unhealthyRows == 0 {
				moduleStatus = "available"
			}
		} else if apiCallAvail || credentialFallback {
			moduleStatus = "partial"
		}
		checkedAt := quotaTime
		if checkedAt.IsZero() {
			checkedAt = credTime
		}
		resp["quota"] = map[string]any{
			"enabled":              true,
			"phase":                phase,
			"full_quota_available": apiCallAvail && supportedRows > 0,
			"credential_fallback":  credentialFallback,
			"module_status":        moduleStatus,
			"last_checked_at":      formatRFC3339OrEmpty(checkedAt),
			"stale_count":          staleCount,
			"error_count":          errorCount,
			"latest_event":         latestQuotaDiagnostic(quotaDiagnostics),
			"last_error":           latestQuotaDiagnosticError(quotaDiagnostics),
		}
	}

	writeJSON(w, resp)
}

func (s *Server) recentQuotaDiagnostics(limit int) []db.QuotaRefreshEventRow {
	if s.db == nil {
		return []db.QuotaRefreshEventRow{}
	}
	rows, err := s.db.RecentQuotaRefreshEvents(time.Now().Add(-72*time.Hour), limit)
	if err != nil {
		return []db.QuotaRefreshEventRow{}
	}
	if rows == nil {
		return []db.QuotaRefreshEventRow{}
	}
	return rows
}

func latestQuotaDiagnostic(rows []db.QuotaRefreshEventRow) map[string]any {
	if len(rows) == 0 {
		return nil
	}
	row := rows[0]
	return map[string]any{
		"checked_at":         row.CheckedAt,
		"provider":           row.Provider,
		"phase":              row.Phase,
		"status":             row.Status,
		"adapter_status":     row.AdapterStatus,
		"error_class":        row.ErrorClass,
		"duration_ms":        row.DurationMs,
		"probe_http_status":  row.ProbeHTTPStatus,
		"probe_error_class":  row.ProbeErrorClass,
		"api_call_reachable": row.APICallReachable > 0,
	}
}

func credentialDiagnosticsSummary(rows []db.CredentialHealthRow) map[string]any {
	providers := map[string]map[string]any{}
	total := len(rows)
	ready, warning, unavailable, disabled, errors := 0, 0, 0, 0, 0
	for _, row := range rows {
		p := providers[row.Provider]
		if p == nil {
			p = map[string]any{
				"provider":          row.Provider,
				"credential_count":  0,
				"ready_count":       0,
				"warning_count":     0,
				"unavailable_count": 0,
				"disabled_count":    0,
				"error_count":       0,
			}
			providers[row.Provider] = p
		}
		p["credential_count"] = p["credential_count"].(int) + 1
		switch row.Status {
		case "ready":
			ready++
			p["ready_count"] = p["ready_count"].(int) + 1
		case "warning":
			warning++
			p["warning_count"] = p["warning_count"].(int) + 1
		case "unavailable":
			unavailable++
			p["unavailable_count"] = p["unavailable_count"].(int) + 1
		case "disabled":
			disabled++
			p["disabled_count"] = p["disabled_count"].(int) + 1
		case "error":
			errors++
			p["error_count"] = p["error_count"].(int) + 1
		}
	}
	return map[string]any{
		"total":              total,
		"ready":              ready,
		"warning":            warning,
		"unavailable":        unavailable,
		"disabled":           disabled,
		"errors":             errors,
		"provider_summaries": sortedSummaryMaps(providers),
	}
}

func quotaDiagnosticsSummary(rows []db.QuotaCurrentRow) map[string]any {
	providers := map[string]map[string]any{}
	supported, unsupported, stale, errors := 0, 0, 0, 0
	for _, row := range rows {
		p := providers[row.Provider]
		if p == nil {
			p = map[string]any{
				"provider":         row.Provider,
				"quota_rows":       0,
				"supported_rows":   0,
				"unsupported_rows": 0,
				"stale_rows":       0,
				"error_rows":       0,
			}
			providers[row.Provider] = p
		}
		p["quota_rows"] = p["quota_rows"].(int) + 1
		if row.QuotaSupported > 0 {
			supported++
			p["supported_rows"] = p["supported_rows"].(int) + 1
		}
		if row.Status == "unsupported" {
			unsupported++
			p["unsupported_rows"] = p["unsupported_rows"].(int) + 1
		}
		if row.Status == "stale" {
			stale++
			p["stale_rows"] = p["stale_rows"].(int) + 1
		}
		if row.Status == "error" {
			errors++
			p["error_rows"] = p["error_rows"].(int) + 1
		}
	}
	return map[string]any{
		"total":              len(rows),
		"supported":          supported,
		"unsupported":        unsupported,
		"stale":              stale,
		"errors":             errors,
		"provider_summaries": sortedSummaryMaps(providers),
	}
}

func sortedSummaryMaps(values map[string]map[string]any) []map[string]any {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}

func latestQuotaDiagnosticError(rows []db.QuotaRefreshEventRow) string {
	for _, row := range rows {
		if row.Status != "error" {
			continue
		}
		if row.ErrorClass != "" {
			return row.ErrorClass
		}
		if row.AdapterStatus != "" {
			return row.AdapterStatus
		}
		return "quota_refresh_failed"
	}
	return ""
}

func getMapVal(m any, key string) any {
	if mm, ok := m.(map[string]any); ok {
		return mm[key]
	}
	return nil
}
