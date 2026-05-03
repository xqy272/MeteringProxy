package webui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/pricing"
	"ai-gateway-metering-proxy/internal/profile"
	"ai-gateway-metering-proxy/internal/store"
	"ai-gateway-metering-proxy/internal/writer"
)

func newTestServer(t *testing.T, basePath string) (*Server, *db.DB) {
	t.Helper()
	path := t.TempDir() + "/test.sqlite"
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	pricingData := pricing.NewPricing()
	bw := writer.New(store.NewEventSink(database), 100, 10, time.Nanosecond)
	bw.Start()
	t.Cleanup(func() { bw.Stop() })

	registry := profile.NewRegistry()

	s := New(database, pricingData, bw, registry, basePath)
	return s, database
}

func TestNewDoesNotPanic(t *testing.T) {
	s1, _ := newTestServer(t, "/metering")
	s2, _ := newTestServer(t, "/stats")
	_ = s1
	_ = s2
}

func TestMeteringExactPath(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET /metering: status %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("GET /metering: Content-Type = %q, want text/html", ct)
	}
}

func TestMeteringSubtreePath(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET /metering/: status %d, want 200", rec.Code)
	}
}

func TestAPISummary(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering/api/summary?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/summary: status %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var data map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Errorf("invalid JSON: %v", err)
	}
}

func TestAPIHealth(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering/api/health", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET /metering/api/health: status %d, want 200", rec.Code)
	}
	var data map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal health: %v", err)
	}
	if _, ok := data["metering_enabled"]; !ok {
		t.Error("health response missing metering_enabled field")
	}
	if _, ok := data["capture_disabled"]; !ok {
		t.Error("health response missing capture_disabled field")
	}
}

func TestAPIMetadata(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering/api/metadata", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/metadata: status %d, want 200", rec.Code)
	}
	var data map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if _, ok := data["endpoints"]; !ok {
		t.Error("metadata response missing endpoints")
	}
	if _, ok := data["ranges"]; !ok {
		t.Error("metadata response missing ranges")
	}
	if _, ok := data["buckets"]; !ok {
		t.Error("metadata response missing buckets")
	}
	endpoints, _ := data["endpoints"].([]interface{})
	if len(endpoints) == 0 {
		t.Error("metadata endpoints list is empty")
	}
}

func TestAPINotFound(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering/api/not-found", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("GET /metering/api/not-found: status %d, want 404", rec.Code)
	}
}

func TestCustomBasePath(t *testing.T) {
	s, _ := newTestServer(t, "/stats")
	req := httptest.NewRequest("GET", "/stats/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET /stats/: status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `const BASE = "/stats/api/";`) {
		t.Errorf("custom basePath not injected into page; body does not contain expected string")
		t.Logf("body snippet: %.200s", body)
	}
}

func TestIndexUsesMetadataDrivenFilters(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "fetchJSON('metadata')") {
		t.Fatal("index should load metadata API")
	}
	if strings.Contains(body, `<option value="/v1/chat/completions">`) ||
		strings.Contains(body, `<option value="/v1/responses">`) {
		t.Fatal("endpoint filters should not be hardcoded in index")
	}
}

func TestCustomBasePathAPI(t *testing.T) {
	s, _ := newTestServer(t, "/stats")
	req := httptest.NewRequest("GET", "/stats/api/health", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET /stats/api/health: status %d, want 200", rec.Code)
	}
}

func TestStaticFileServed(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("status %d, want 200", rec.Code)
	}
}

func TestAPIRequestsStatusCategories(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC()
	records := []db.UsageRecord{
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 200, LatencyMs: 10, TTFBMs: 3},
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 201, LatencyMs: 11, TTFBMs: 4},
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 302, LatencyMs: 12, TTFBMs: 5},
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 400, LatencyMs: 13, TTFBMs: 6},
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 429, LatencyMs: 14, TTFBMs: 7},
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 500, LatencyMs: 15, TTFBMs: 8},
	}
	if err := database.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	cases := []struct {
		status string
		want   []int
	}{
		{"success", []int{201, 200}},
		{"4xx", []int{429, 400}},
		{"5xx", []int{500}},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/metering/api/requests?range=24h&status="+tc.status, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status %s: HTTP %d, want 200", tc.status, rec.Code)
		}
		var rows []event.RequestReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("status %s: unmarshal: %v", tc.status, err)
		}
		if len(rows) != len(tc.want) {
			t.Fatalf("status %s: got %d rows, want %d: %+v", tc.status, len(rows), len(tc.want), rows)
		}
		for i, want := range tc.want {
			if rows[i].Status != want {
				t.Fatalf("status %s row %d = %d, want %d", tc.status, i, rows[i].Status, want)
			}
		}
	}
}

func TestAPIRequestsRangeAndTTFB(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.InsertBatch([]db.UsageRecord{
		{CreatedAt: now.Add(-48 * time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 200, LatencyMs: 100, TTFBMs: 30, TotalTokens: 10},
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 200, LatencyMs: 50, TTFBMs: 12, TotalTokens: 20},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/requests?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("24h HTTP %d, want 200", rec.Code)
	}
	var rows []event.RequestReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal 24h: %v", err)
	}
	if len(rows) != 1 || rows[0].TotalTokens != 20 || rows[0].TTFBMs != 12 {
		t.Fatalf("24h rows = %+v, want only recent row with ttfb_ms=12", rows)
	}

	req = httptest.NewRequest("GET", "/metering/api/requests?range=7d", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal 7d: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("7d rows = %d, want 2", len(rows))
	}
}

func TestAPIErrorsSource(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC()
	if err := database.InsertBatch([]db.UsageRecord{
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 500, LatencyMs: 100},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/errors?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var resp struct {
		Source   string                      `json:"source"`
		Timeline []event.ErrorTimelineReport `json:"timeline"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal fallback errors: %v", err)
	}
	if resp.Source != "request_usage_fallback" || len(resp.Timeline) != 1 {
		t.Fatalf("fallback response = %+v, want request_usage_fallback with one row", resp)
	}

	if err := database.InsertHealthMetric(now.Format(time.RFC3339), 1, 2, 3, 4, 0); err != nil {
		t.Fatalf("InsertHealthMetric: %v", err)
	}
	req = httptest.NewRequest("GET", "/metering/api/errors?range=24h", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal health errors: %v", err)
	}
	if resp.Source != "health_metrics" || len(resp.Timeline) != 1 {
		t.Fatalf("health response = %+v, want health_metrics with one row", resp)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
