package webui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/report"
	"ai-gateway-metering-proxy/internal/writer"
)

//go:embed static/*
var staticFiles embed.FS

// QuotaDiagnosticsReader is the narrow DB surface for quota diagnostic lists.
// Defined by webui as the consumer; *db.DB implements it.
type QuotaDiagnosticsReader interface {
	RecentQuotaRefreshEvents(ctx context.Context, since time.Time, limit int) ([]db.QuotaRefreshEventRow, error)
}

// ObservabilityReader is the narrow DB surface for observability capture/side-channel counts.
type ObservabilityReader interface {
	SideUsageStatusCounts(ctx context.Context, since time.Time) (map[string]int64, error)
	CaptureOutcomeCounts(ctx context.Context, since time.Time) (captured, skipped, failed int64, err error)
}

// DiagnosticsReaders groups remaining WebUI-owned CPA/observability DB reads.
type DiagnosticsReaders struct {
	Quota QuotaDiagnosticsReader
	Obs   ObservabilityReader
}

type apiRoute struct {
	method  string
	handler http.HandlerFunc
}

type Server struct {
	reports   report.CoreReporter
	quotaDiag QuotaDiagnosticsReader
	obs       ObservabilityReader
	writer    *writer.BatchWriter
	basePath  string
	apiPrefix string
	mux       *http.ServeMux
	apiRoutes map[string]apiRoute

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
func New(reports report.CoreReporter, batchWriter *writer.BatchWriter, basePath string, diag DiagnosticsReaders) *Server {
	staticFS, _ := fs.Sub(staticFiles, "static")
	return newServer(reports, batchWriter, basePath, staticFS, diag)
}

// NewWithStaticFS creates a Server that serves static files from the given
// filesystem. Use this for local development (os.DirFS on the static/ directory)
// or testing with custom asset sets.
func NewWithStaticFS(reports report.CoreReporter, batchWriter *writer.BatchWriter, basePath string, staticFS fs.FS, diag DiagnosticsReaders) *Server {
	return newServer(reports, batchWriter, basePath, staticFS, diag)
}

func newServer(reports report.CoreReporter, batchWriter *writer.BatchWriter, basePath string, staticFS fs.FS, diag DiagnosticsReaders) *Server {
	if reports == nil {
		panic("webui: core reporter is required")
	}
	basePath = strings.TrimRight(basePath, "/")
	apiPrefix := basePath + "/api/"

	s := &Server{
		reports:         reports,
		quotaDiag:       diag.Quota,
		obs:             diag.Obs,
		writer:          batchWriter,
		basePath:        basePath,
		apiPrefix:       apiPrefix,
		staticFS:        staticFS,
		meteringEnabled: func() bool { return true },
		mux:             http.NewServeMux(),
	}
	s.registerAPIRoutes()

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

func (s *Server) registerAPIRoutes() {
	s.apiRoutes = map[string]apiRoute{
		"overview":                   {http.MethodGet, s.handleOverview},
		"issues":                     {http.MethodGet, s.handleIssues},
		"summary":                    {http.MethodGet, s.handleSummary},
		"timeseries":                 {http.MethodGet, s.handleTimeseries},
		"activity":                   {http.MethodGet, s.handleActivity},
		"models":                     {http.MethodGet, s.handleModels},
		"keys":                       {http.MethodGet, s.handleKeys},
		"requests":                   {http.MethodGet, s.handleRequests},
		"multimodal/summary":         {http.MethodGet, s.handleMultimodalSummary},
		"images/summary":             {http.MethodGet, s.handleImageSummary},
		"images/models":              {http.MethodGet, s.handleImageModels},
		"images/requests":            {http.MethodGet, s.handleImageRequests},
		"errors":                     {http.MethodGet, s.handleErrors},
		"health":                     {http.MethodGet, s.handleHealth},
		"metadata":                   {http.MethodGet, s.handleMetadata},
		"quota/diagnostics":          {http.MethodGet, s.handleQuotaDiagnostics},
		"quota":                      {http.MethodGet, s.handleQuota},
		"quota/refresh":              {http.MethodPost, s.handleQuotaRefresh},
		"observability":              {http.MethodGet, s.handleObservability},
		"gateway/capabilities":       {http.MethodGet, s.handleGatewayCapabilities},
		"model-assets":               {http.MethodGet, s.handleModelAssets},
		"pricing/stub":               {http.MethodGet, s.handlePricingStub},
		"cpa/auth":                   {http.MethodGet, s.handleCPAAuth},
		"cpa/auth/refresh":           {http.MethodPost, s.handleCPAAuthRefresh},
		"cpa/cooldown/reset":         {http.MethodPost, s.handleCPACooldownReset},
		"provider-quota":             {http.MethodGet, s.handleProviderQuota},
		"provider-quota/refresh":     {http.MethodPost, s.handleProviderQuotaRefresh},
		"provider-quota/diagnostics": {http.MethodGet, s.handleProviderQuotaDiagnostics},
	}
}

func (s *Server) routeAPI(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	rel, ok := s.apiRelativePath(r.URL.Path)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	route, ok := s.apiRoutes[rel]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if r.Method != route.method {
		w.Header().Set("Allow", route.method)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	route.handler(w, r)
}

func (s *Server) apiRelativePath(path string) (string, bool) {
	if !strings.HasPrefix(path, s.apiPrefix) {
		return "", false
	}
	return path[len(s.apiPrefix):], true
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

func parseRange(r *http.Request) (time.Time, string, error) {
	rangeStr := r.URL.Query().Get("range")
	if rangeStr == "" {
		return time.Now().Add(-24 * time.Hour), "24h", nil
	}
	switch rangeStr {
	case "24h":
		return time.Now().Add(-24 * time.Hour), rangeStr, nil
	case "7d":
		return time.Now().Add(-7 * 24 * time.Hour), rangeStr, nil
	case "30d":
		return time.Now().Add(-30 * 24 * time.Hour), rangeStr, nil
	case "today":
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), rangeStr, nil
	default:
		return time.Time{}, "", errInvalidRange
	}
}

func parseKeyHashFilter(r *http.Request) (string, error) {
	value := r.URL.Query().Get("key_hash")
	if err := report.ValidateKeyHashFilter(value); err != nil {
		return "", errInvalidKeyHash
	}
	return value, nil
}

func parseBucketMinutes(r *http.Request) (int, error) {
	bucketStr := r.URL.Query().Get("bucket")
	if bucketStr == "" {
		return 60, nil
	}
	switch bucketStr {
	case "1h":
		return 60, nil
	case "1d":
		return 1440, nil
	default:
		return 0, errInvalidFilter
	}
}

// parseLimit accepts only an explicit base-10 integer in [1, max]. Absent keeps defaultLimit.
func parseLimit(r *http.Request, defaultLimit, max int) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.ParseInt(raw, 10, 0)
	if err != nil {
		return 0, errInvalidFilter
	}
	if n <= 0 || int(n) > max {
		return 0, errInvalidFilter
	}
	return int(n), nil
}

func parseStatusFilter(r *http.Request) (statusMin, statusMax int, err error) {
	status := r.URL.Query().Get("status")
	if status == "" {
		return 0, 0, nil
	}
	switch status {
	case "success":
		return 200, 300, nil
	case "4xx":
		return 400, 500, nil
	case "5xx":
		return 500, 0, nil
	default:
		return 0, 0, errInvalidFilter
	}
}

func parseNonzero(r *http.Request) (bool, error) {
	raw := r.URL.Query().Get("nonzero")
	if raw == "" {
		return false, nil
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errInvalidFilter
	}
}

var (
	errInvalidRange   = errors.New("invalid_range")
	errInvalidKeyHash = errors.New("invalid_key_hash")
	errInvalidFilter  = errors.New("invalid_filter")
)

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("writeJSON encode/write failed: %v", err)
	}
}

type apiErrorBody struct {
	Error apiErrorDetail `json:"error"`
}

type apiErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(apiErrorBody{Error: apiErrorDetail{Code: code, Message: message}}); err != nil {
		log.Printf("writeAPIError encode failed: %v", err)
	}
}

func writeReportQueryFailed(w http.ResponseWriter, reportName string, err error) {
	log.Printf("%s report query failed: %v", reportName, err)
	writeAPIError(w, http.StatusInternalServerError, "report_query_failed", "failed to load "+reportName+" report")
}

func writeInvalidRange(w http.ResponseWriter) {
	writeAPIError(w, http.StatusBadRequest, "invalid_range", "range must be one of 24h, today, 7d, 30d")
}

func writeInvalidKeyHash(w http.ResponseWriter) {
	writeAPIError(w, http.StatusBadRequest, "invalid_key_hash", "key_hash must be a 64-character lowercase hex value")
}

func writeInvalidFilter(w http.ResponseWriter) {
	writeAPIError(w, http.StatusBadRequest, "invalid_filter", "invalid query filter")
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
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	result, err := s.reports.Summary(r.Context(), report.SummaryFilter{Since: since})
	if err != nil {
		writeReportQueryFailed(w, "summary", err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	keyHash, err := parseKeyHashFilter(r)
	if err != nil {
		writeInvalidKeyHash(w)
		return
	}
	bucketMin, err := parseBucketMinutes(r)
	if err != nil {
		writeInvalidFilter(w)
		return
	}
	rows, err := s.reports.Timeseries(r.Context(), report.TimeseriesFilter{Since: since, BucketMin: bucketMin, KeyHash: keyHash})
	if err != nil {
		writeReportQueryFailed(w, "timeseries", err)
		return
	}
	if rows == nil {
		rows = []report.TimeseriesReport{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	keyHash, err := parseKeyHashFilter(r)
	if err != nil {
		writeInvalidKeyHash(w)
		return
	}
	row, err := s.reports.Activity(r.Context(), report.ActivityFilter{Since: since, KeyHash: keyHash})
	if err != nil {
		writeReportQueryFailed(w, "activity", err)
		return
	}
	writeJSON(w, row)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	keyHash, err := parseKeyHashFilter(r)
	if err != nil {
		writeInvalidKeyHash(w)
		return
	}
	models, err := s.reports.Models(r.Context(), report.ModelsFilter{Since: since, KeyHash: keyHash})
	if err != nil {
		writeReportQueryFailed(w, "models", err)
		return
	}
	if models == nil {
		models = []report.ModelReport{}
	}
	writeJSON(w, models)
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	rows, err := s.reports.Keys(r.Context(), report.KeysFilter{Since: since})
	if err != nil {
		writeReportQueryFailed(w, "keys", err)
		return
	}
	if rows == nil {
		rows = []report.KeyReport{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	keyHash, err := parseKeyHashFilter(r)
	if err != nil {
		writeInvalidKeyHash(w)
		return
	}
	limit, err := parseLimit(r, 100, 500)
	if err != nil {
		writeInvalidFilter(w)
		return
	}
	statusMin, statusMax, err := parseStatusFilter(r)
	if err != nil {
		writeInvalidFilter(w)
		return
	}
	model := r.URL.Query().Get("model")
	endpoint := r.URL.Query().Get("endpoint")
	errorClass := r.URL.Query().Get("error_class")
	rows, err := s.reports.Requests(r.Context(), report.RequestFilter{
		Since: since, KeyHash: keyHash, Limit: limit,
		StatusMin: statusMin, StatusMax: statusMax,
		Model: model, Endpoint: endpoint, ErrorClass: errorClass,
	})
	if err != nil {
		writeReportQueryFailed(w, "requests", err)
		return
	}
	if rows == nil {
		rows = []report.RequestReport{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleMultimodalSummary(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	rows, err := s.reports.MultimodalSummary(r.Context(), report.MultimodalFilter{Since: since})
	if err != nil {
		writeReportQueryFailed(w, "multimodal summary", err)
		return
	}
	if rows == nil {
		rows = []report.MultimodalSummaryReport{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleImageSummary(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	result, err := s.reports.ImageSummary(r.Context(), report.ImagesFilter{Since: since})
	if err != nil {
		writeReportQueryFailed(w, "image summary", err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleImageModels(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	rows, err := s.reports.ImageModels(r.Context(), report.ImagesFilter{Since: since})
	if err != nil {
		writeReportQueryFailed(w, "image models", err)
		return
	}
	if rows == nil {
		rows = []report.ImageModelReport{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleImageRequests(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	limit, err := parseLimit(r, 100, 500)
	if err != nil {
		writeInvalidFilter(w)
		return
	}
	rows, err := s.reports.ImageRequests(r.Context(), report.ImageRequestsFilter{Since: since, Limit: limit})
	if err != nil {
		writeReportQueryFailed(w, "image requests", err)
		return
	}
	if rows == nil {
		rows = []report.RequestReport{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleErrors(w http.ResponseWriter, r *http.Request) {
	since, _, err := parseRange(r)
	if err != nil {
		writeInvalidRange(w)
		return
	}
	nonzero, err := parseNonzero(r)
	if err != nil {
		writeInvalidFilter(w)
		return
	}
	result, err := s.reports.Errors(r.Context(), report.ErrorsFilter{Since: since, Nonzero: nonzero})
	if err != nil {
		writeReportQueryFailed(w, "errors", err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	result, err := s.reports.Health(r.Context(), report.HealthFilter{MeteringEnabled: s.meteringEnabled()})
	if err != nil {
		writeReportQueryFailed(w, "health", err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	result, err := s.reports.Metadata(r.Context(), report.MetadataFilter{})
	if err != nil {
		writeReportQueryFailed(w, "metadata", err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	diagnostics := []db.QuotaRefreshEventRow{}
	diagStatus := "not_applicable"
	diagPartial := false
	if s.quotaPoller != nil {
		var err error
		diagnostics, diagStatus, diagPartial, err = s.recentQuotaDiagnostics(r.Context(), 12)
		if err != nil {
			writeReportQueryFailed(w, "quota diagnostics", err)
			return
		}
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
			"diagnostics_status":   diagStatus,
			"partial":              diagPartial,
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
			"diagnostics_status":   diagStatus,
			"partial":              diagPartial,
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
		"diagnostics_status":   diagStatus,
		"partial":              diagPartial,
	})
}

func (s *Server) handleQuotaRefresh(w http.ResponseWriter, r *http.Request) {
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
	limit, err := parseLimit(r, 50, 100)
	if err != nil {
		writeInvalidFilter(w)
		return
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
	events, eventsStatus, eventsPartial, err := s.recentQuotaDiagnostics(r.Context(), limit)
	if err != nil {
		writeReportQueryFailed(w, "quota diagnostics", err)
		return
	}
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
		"events_status":             eventsStatus,
		"partial":                   eventsPartial,
	})
}

func (s *Server) handleObservability(w http.ResponseWriter, r *http.Request) {
	credRows := []db.CredentialHealthRow{}
	credTime := time.Time{}
	quotaDiagnostics := []db.QuotaRefreshEventRow{}
	quotaDiagStatus := "not_applicable"
	quotaDiagPartial := false
	if s.quotaPoller != nil {
		var err error
		quotaDiagnostics, quotaDiagStatus, quotaDiagPartial, err = s.recentQuotaDiagnostics(r.Context(), 6)
		if err != nil {
			writeReportQueryFailed(w, "observability", err)
			return
		}
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
		side := map[string]any{
			"enabled":          true,
			"connected":        connected,
			"merge_mode":       s.correlationMode,
			"last_event_at":    formatRFC3339OrEmpty(lastAt),
			"last_received_at": formatRFC3339OrEmpty(lastAt),
			"last_error":       lastErr,
			"matched_1h":       int64(0),
			"unmatched_1h":     int64(0),
			"stored_only_1h":   int64(0),
			"conflicts_1h":     int64(0),
			"counts_status":    "not_applicable",
		}
		if s.obs != nil {
			counts, err := s.obs.SideUsageStatusCounts(r.Context(), time.Now().Add(-1*time.Hour))
			if err != nil {
				if isContextError(err) {
					writeReportQueryFailed(w, "observability", err)
					return
				}
				log.Printf("observability side usage counts failed: %v", err)
				side["counts_status"] = "unavailable"
				side["partial"] = true
			} else {
				side["counts_status"] = "complete"
				side["matched_1h"] = counts["matched"]
				side["unmatched_1h"] = counts["unmatched"]
				side["stored_only_1h"] = counts["stored_only"]
				side["conflicts_1h"] = counts["conflict"]
			}
		}
		resp["side_channel"] = side
	}

	if s.writer != nil {
		_, dropped, parseErrors, _ := s.writer.Snapshot()
		resp["request_capture"] = map[string]any{
			"parse_errors":   parseErrors,
			"dropped_events": dropped,
		}
	}

	captured1h, skipped1h, failed1h := int64(0), int64(0), int64(0)
	captureStatus := "not_applicable"
	capturePartial := false
	if s.obs != nil {
		var err error
		captured1h, skipped1h, failed1h, err = s.obs.CaptureOutcomeCounts(r.Context(), time.Now().Add(-1*time.Hour))
		if err != nil {
			if isContextError(err) {
				writeReportQueryFailed(w, "observability", err)
				return
			}
			log.Printf("observability capture outcome counts failed: %v", err)
			captureStatus = "unavailable"
			capturePartial = true
			captured1h, skipped1h, failed1h = 0, 0, 0
		} else {
			captureStatus = "complete"
		}
	}
	resp["request_capture"] = map[string]any{
		"parse_errors":       getMapVal(resp["request_capture"], "parse_errors"),
		"dropped_events":     getMapVal(resp["request_capture"], "dropped_events"),
		"captured_1h":        captured1h,
		"skipped_1h":         skipped1h,
		"failed_1h":          failed1h,
		"capture_skipped_1h": skipped1h,
		"capture_failed_1h":  failed1h,
		"counts_status":      captureStatus,
		"partial":            capturePartial,
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
			"diagnostics_status":   quotaDiagStatus,
			"partial":              quotaDiagPartial,
		}
	}

	writeJSON(w, resp)
}

func (s *Server) recentQuotaDiagnostics(ctx context.Context, limit int) (rows []db.QuotaRefreshEventRow, status string, partial bool, err error) {
	if s.quotaDiag == nil {
		return []db.QuotaRefreshEventRow{}, "not_applicable", false, nil
	}
	rows, err = s.quotaDiag.RecentQuotaRefreshEvents(ctx, time.Now().Add(-72*time.Hour), limit)
	if err != nil {
		if isContextError(err) {
			return nil, "", false, err
		}
		log.Printf("quota diagnostics query failed: %v", err)
		return []db.QuotaRefreshEventRow{}, "unavailable", true, nil
	}
	if rows == nil {
		rows = []db.QuotaRefreshEventRow{}
	}
	return rows, "complete", false, nil
}

func isContextError(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
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
