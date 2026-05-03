package webui

import (
	"embed"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

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

	meteringEnabled func() bool
}

func New(database store.ReportStore, pricingData *pricing.Pricing, batchWriter *writer.BatchWriter, registry *profile.Registry, basePath string) *Server {
	basePath = strings.TrimRight(basePath, "/")
	apiPrefix := basePath + "/api/"

	s := &Server{
		db:              database,
		pricing:         pricingData,
		writer:          batchWriter,
		registry:        registry,
		basePath:        basePath,
		apiPrefix:       apiPrefix,
		meteringEnabled: func() bool { return true },
		mux:             http.NewServeMux(),
	}

	s.mux.HandleFunc(basePath, s.handleIndex)

	staticFS, _ := fs.Sub(staticFiles, "static")
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
		fileServer.ServeHTTP(w, r)
	})

	return s
}

// SetMeteringEnabledFunc sets the callback used to report metering state.
func (s *Server) SetMeteringEnabledFunc(fn func() bool) {
	s.meteringEnabled = fn
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routeAPI(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/api/summary"):
		s.handleSummary(w, r)
	case strings.HasSuffix(path, "/api/timeseries"):
		s.handleTimeseries(w, r)
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
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	htmlBytes, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index not found", 500)
		return
	}
	apiBase := html.EscapeString(s.basePath + "/api/")
	injected := strings.ReplaceAll(string(htmlBytes), "__METERING_API_BASE__", apiBase)
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
		cost, _ := s.pricing.Cost(m.Model, m.InputTokens, m.OutputTokens, m.ReasoningTokens, m.CachedTokens)
		totalCost += cost
	}
	report := event.SummaryFromDB(row)
	report.TotalCost = totalCost
	writeJSON(w, report)
}

func (s *Server) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	bucketStr := r.URL.Query().Get("bucket")
	bucketMin := 10
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
	writeJSON(w, report)
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
		cost, known := s.pricing.Cost(report[i].Model, report[i].InputTokens, report[i].OutputTokens, report[i].ReasoningTokens, report[i].CachedTokens)
		report[i].Cost = cost
		report[i].CostKnown = known
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
	since, _ := parseRange(r)

	rows, err := s.db.Requests(limit, statusMin, statusMax, model, endpoint, since)
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
		Timeline      []event.ErrorTimelineReport `json:"timeline"`
		Source        string                      `json:"source"`
		QueueDepth    int64                       `json:"queue_depth"`
		ParseErrors   int64                       `json:"parse_errors"`
		DBErrors      int64                       `json:"db_errors"`
		DroppedEvents int64                       `json:"dropped_events"`
	}

	writeErrResp := func(source string, timeline []event.ErrorTimelineReport) {
		latestHealth, _ := s.db.LatestHealth()
		resp := errorResp{
			Timeline: timeline,
			Source:   source,
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

	healthRows, err := s.db.ErrorTimeline(since)
	if err != nil || len(healthRows) == 0 {
		reqErrorRows, _ := s.db.ErrorTimelineFromRequests(since)
		writeErrResp("request_usage_fallback", event.ErrorTimelineFromDB(reqErrorRows))
		return
	}

	writeErrResp("health_metrics", event.ErrorTimelineFromDB(healthRows))
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
			{Key: "24h", Label: "Last 24 Hours", Bucket: "10m"},
			{Key: "today", Label: "Today", Bucket: "10m"},
			{Key: "7d", Label: "Last 7 Days", Bucket: "1h"},
			{Key: "30d", Label: "Last 30 Days", Bucket: "1d"},
		},
		Buckets: []event.BucketMeta{
			{Key: "10m", Label: "10 Minutes"},
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
