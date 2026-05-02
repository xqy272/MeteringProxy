package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/pricing"
	"ai-gateway-metering-proxy/internal/writer"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	db        *db.DB
	pricing   *pricing.Pricing
	writer    *writer.BatchWriter
	basePath  string // e.g. "/metering"
	apiPrefix string // e.g. "/metering/api/"
	mux       *http.ServeMux
}

func New(database *db.DB, pricingData *pricing.Pricing, batchWriter *writer.BatchWriter, basePath string) *Server {
	// Normalize: no trailing slash in basePath, slash in apiPrefix and subtreePrefix.
	basePath = strings.TrimRight(basePath, "/")
	apiPrefix := basePath + "/api/"
	subtreePattern := basePath + "/"

	s := &Server{
		db:        database,
		pricing:   pricingData,
		writer:    batchWriter,
		basePath:  basePath,
		apiPrefix: apiPrefix,
		mux:       http.NewServeMux(),
	}

	// Exact match for /metering (without trailing slash): serve index.
	s.mux.HandleFunc(basePath, s.handleIndex)

	// Subtree for /metering/* dispatches API, static files, and the index.
	staticFS, _ := fs.Sub(staticFiles, "static")
	fileServer := http.StripPrefix(basePath, http.FileServer(http.FS(staticFS)))

	s.mux.HandleFunc(subtreePattern, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, apiPrefix) {
			s.routeAPI(w, r)
			return
		}
		// /metering/ with no sub-path serves index.
		if path == subtreePattern || path == basePath {
			s.handleIndex(w, r)
			return
		}
		// Everything else is served as a static file.
		fileServer.ServeHTTP(w, r)
	})

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// routeAPI dispatches to the correct API handler.
func (s *Server) routeAPI(w http.ResponseWriter, r *http.Request) {
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
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	html, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index not found", 500)
		return
	}
	apiBase, _ := json.Marshal(s.basePath + "/api/")
	injected := strings.Replace(string(html),
		"const BASE = '/metering/api/';",
		fmt.Sprintf("const BASE = %s;", apiBase),
		1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
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
	row.TotalCost = totalCost
	writeJSON(w, row)
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
	if rows == nil {
		rows = []db.TimeseriesRow{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	rows, err := s.db.Models(since)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if rows == nil {
		rows = []db.ModelRow{}
	}
	type modelWithCost struct {
		db.ModelRow
		Cost      float64 `json:"cost"`
		CostKnown bool    `json:"cost_known"`
	}
	result := make([]modelWithCost, len(rows))
	for i, row := range rows {
		cost, known := s.pricing.Cost(row.Model, row.InputTokens, row.OutputTokens, row.ReasoningTokens, row.CachedTokens)
		result[i] = modelWithCost{
			ModelRow:  row,
			Cost:      cost,
			CostKnown: known,
		}
	}
	writeJSON(w, result)
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)
	rows, err := s.db.Keys(since)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if rows == nil {
		rows = []db.KeyRow{}
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
	since, _ := parseRange(r)

	rows, err := s.db.Requests(limit, statusMin, statusMax, model, endpoint, since)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if rows == nil {
		rows = []db.RequestRow{}
	}
	writeJSON(w, rows)
}

func (s *Server) handleErrors(w http.ResponseWriter, r *http.Request) {
	since, _ := parseRange(r)

	type errorResp struct {
		Timeline      []db.ErrorTimelineRow `json:"timeline"`
		Source        string                `json:"source"`
		QueueDepth    int64                 `json:"queue_depth"`
		ParseErrors   int64                 `json:"parse_errors"`
		DBErrors      int64                 `json:"db_errors"`
		DroppedEvents int64                 `json:"dropped_events"`
	}

	writeErrResp := func(source string, timeline []db.ErrorTimelineRow) {
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
			resp.Timeline = []db.ErrorTimelineRow{}
		}
		writeJSON(w, resp)
	}

	healthRows, err := s.db.ErrorTimeline(since)
	if err != nil || len(healthRows) == 0 {
		reqErrorRows, _ := s.db.ErrorTimelineFromRequests(since)
		writeErrResp("request_usage_fallback", reqErrorRows)
		return
	}

	writeErrResp("health_metrics", healthRows)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	queueDepth, dropped, parseErrors, dbErrors := s.writer.Snapshot()
	latestHealth, _ := s.db.LatestHealth()
	resp := map[string]any{
		"queue_depth":     queueDepth,
		"dropped_events":  dropped,
		"parse_errors":    parseErrors,
		"db_write_errors": dbErrors,
		"latest_health":   latestHealth,
	}
	writeJSON(w, resp)
}
