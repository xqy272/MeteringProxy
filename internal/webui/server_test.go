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

func TestAPIResponsesAreNotCacheable(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering/api/summary?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want no-cache", got)
	}
	if got := rec.Header().Get("Expires"); got != "0" {
		t.Errorf("Expires = %q, want 0", got)
	}
}

func TestIndexRequiresRevalidation(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
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

func TestAPIActivity(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.InsertBatch([]db.UsageRecord{
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 200, LatencyMs: 100, TTFBMs: 20, CaptureOutcome: "captured"},
		{CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 500, LatencyMs: 300, TTFBMs: 60, CaptureOutcome: "failed", Error: "upstream"},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/activity?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/activity: status %d, want 200", rec.Code)
	}
	var data event.ActivityReport
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("unmarshal activity: %v", err)
	}
	if data.SampleSize != 2 || data.FailedCount != 1 || data.P95LatencyMs != 300 || data.LatestErrorStatus != 500 {
		t.Fatalf("activity response = %+v, want sample=2 failed=1 p95=300 latest=500", data)
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
	if !strings.Contains(body, `content="/stats/api/"`) {
		t.Errorf("custom basePath not injected into page; body does not contain expected string")
		t.Logf("body snippet: %.200s", body)
	}
	if !strings.Contains(body, `href="/stats/styles.css"`) {
		t.Errorf("custom basePath not injected into stylesheet URL")
	}
	if !strings.Contains(body, `href="/stats/mark.svg"`) {
		t.Errorf("custom basePath not injected into favicon URL")
	}
	if !strings.Contains(body, `src="/stats/app.js"`) {
		t.Errorf("custom basePath not injected into script URL")
	}
	if !strings.Contains(body, `src="/stats/i18n.js"`) {
		t.Errorf("custom basePath not injected into i18n script URL")
	}
	if !strings.Contains(body, `src="/stats/mark.svg"`) {
		t.Errorf("custom basePath not injected into brand image URL")
	}
	if !strings.Contains(body, `id="language-select"`) {
		t.Errorf("language selector missing from WebUI")
	}
	if !strings.Contains(body, `<html lang="en">`) {
		t.Errorf("WebUI static fallback should default to English")
	}
	if !strings.Contains(body, `https://github.com/xqy272/MeteringProxy`) {
		t.Errorf("GitHub link missing from WebUI")
	}
	if strings.Contains(body, "__METERING_API_BASE__") || strings.Contains(body, "__METERING_BASE__") {
		t.Errorf("api base placeholder was not replaced")
	}
}

func TestIndexUsesMetadataDrivenFilters(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering/app.js", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "fetchJSON('metadata')") {
		t.Fatal("index should load metadata API")
	}
	if !strings.Contains(body, "window.METERING_I18N") {
		t.Fatal("app script should read the lightweight i18n dictionary")
	}
	if !strings.Contains(body, "fetchJSON('activity?range='") {
		t.Fatal("index should load activity API")
	}
	if !strings.Contains(body, "Promise.allSettled") {
		t.Fatal("index should tolerate partial API failures")
	}
	if !strings.Contains(body, "cache: 'no-store'") {
		t.Fatal("index fetches should bypass browser cache")
	}
	if !strings.Contains(body, `meta[name="api-base"]`) {
		t.Fatal("index should read API base from injected metadata")
	}
	if !strings.Contains(body, "data-tooltip") || !strings.Contains(body, "chart-tooltip") {
		t.Fatal("charts should use custom tooltip plumbing")
	}
	if strings.Contains(body, `<option value="/v1/chat/completions">`) ||
		strings.Contains(body, `<option value="/v1/responses">`) {
		t.Fatal("endpoint filters should not be hardcoded in app script")
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
	for _, path := range []string{"/metering/", "/metering/styles.css", "/metering/i18n.js", "/metering/app.js", "/metering/mark.svg"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("%s status %d, want 200", path, rec.Code)
		}
		if path != "/metering/" && rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s Cache-Control = %q, want no-cache", path, rec.Header().Get("Cache-Control"))
		}
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
		Source             string                      `json:"source"`
		BucketCount        int                         `json:"bucket_count"`
		NonzeroBucketCount int                         `json:"nonzero_bucket_count"`
		Timeline           []event.ErrorTimelineReport `json:"timeline"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal fallback errors: %v", err)
	}
	if resp.Source != "request_usage" || len(resp.Timeline) != 1 {
		t.Fatalf("fallback response = %+v, want request_usage with one row", resp)
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
	if resp.Source != "health_metrics+request_usage" || len(resp.Timeline) != 2 {
		t.Fatalf("health response = %+v, want combined health and request rows", resp)
	}
	if resp.BucketCount != 2 || resp.NonzeroBucketCount != 2 {
		t.Fatalf("health bucket counts = %+v, want 2/2", resp)
	}

	if err := database.InsertHealthMetric(now.Add(time.Minute).Format(time.RFC3339), 1, 2, 3, 4, 0); err != nil {
		t.Fatalf("InsertHealthMetric no-change: %v", err)
	}
	req = httptest.NewRequest("GET", "/metering/api/errors?range=24h&nonzero=true", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal nonzero errors: %v", err)
	}
	if len(resp.Timeline) != 2 || resp.BucketCount != 3 || resp.NonzeroBucketCount != 2 {
		t.Fatalf("nonzero health response = %+v, want request error plus one nonzero health bucket", resp)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
