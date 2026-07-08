package webui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
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

func TestNewWithStaticFS_ServesIndexFromInjectedFS(t *testing.T) {
	database, err := db.Open(t.TempDir() + "/test.sqlite")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	pricingData := pricing.NewPricing()
	bw := writer.New(store.NewEventSink(database), 100, 10, time.Nanosecond)
	bw.Start()
	t.Cleanup(func() { bw.Stop() })

	registry := profile.NewRegistry()

	staticFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<meta name="api-base" content="__METERING_API_BASE__"><script src="__METERING_BASE__/app.js"></script>`),
		},
		"app.js": &fstest.MapFile{
			Data: []byte(`console.log("hello");`),
		},
	}

	s := NewWithStaticFS(database, pricingData, bw, registry, "/metering", staticFS)

	// Index: placeholder injection.
	req := httptest.NewRequest("GET", "/metering", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("index status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "__METERING_API_BASE__") {
		t.Fatal("placeholder __METERING_API_BASE__ was not replaced")
	}
	if strings.Contains(body, "__METERING_BASE__") {
		t.Fatal("placeholder __METERING_BASE__ was not replaced")
	}
	if !strings.Contains(body, `content="/metering/api/"`) {
		t.Fatal("api-base not injected into meta tag")
	}
	if !strings.Contains(body, `src="/metering/app.js"`) {
		t.Fatal("base path not injected into script URL")
	}

	// Static file: fileServer branch.
	req = httptest.NewRequest("GET", "/metering/app.js", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("app.js status %d, want 200", rec.Code)
	}
	if rec.Body.String() != `console.log("hello");` {
		t.Fatal("app.js body mismatch")
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("app.js Cache-Control = %q, want no-cache", rec.Header().Get("Cache-Control"))
	}
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

func TestAPIModelsCostIncludesCacheCreationTokens(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	s.pricing.Models["claude-sonnet-4-6"] = pricing.ModelPrice{
		InputPer1M:         3.00,
		CachedInputPer1M:   0.30,
		CacheCreationPer1M: 3.75,
		OutputPer1M:        15.00,
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.InsertBatch([]db.UsageRecord{
		{
			CreatedAt:           now.Add(-10 * time.Minute).Format(time.RFC3339),
			Endpoint:            "/v1/messages",
			Method:              "POST",
			Status:              200,
			LatencyMs:           100,
			ModelReturned:       "claude-sonnet-4-6",
			InputTokens:         125,
			OutputTokens:        30,
			CachedTokens:        20,
			CacheCreationTokens: 5,
			TotalTokens:         155,
		},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/models?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/models: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var rows []event.ModelReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal models: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("models = %+v, want one row", rows)
	}
	expected := 100/1_000_000.0*3.00 + 20/1_000_000.0*0.30 + 5/1_000_000.0*3.75 + 30/1_000_000.0*15.00
	if diff := rows[0].Cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("cost = %.6f, want %.6f", rows[0].Cost, expected)
	}
	if rows[0].CacheCreationTokens != 5 {
		t.Fatalf("cache_creation_tokens = %d, want 5", rows[0].CacheCreationTokens)
	}
}

func TestAPIModelsDoesNotMarkZeroTokenUnknownModelUnpriced(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.InsertBatch([]db.UsageRecord{{
		CreatedAt:      now.Add(-10 * time.Minute).Format(time.RFC3339),
		Endpoint:       "/v1/images/variations",
		Method:         "POST",
		Status:         200,
		LatencyMs:      100,
		ModelRequested: "unconfigured-request-only-model",
		CaptureMode:    event.CaptureRequestOnly,
		MeteringKind:   event.MeteringRequestOnly,
		CaptureOutcome: event.OutcomeSkipped,
		CaptureReason:  event.ReasonRequestOnlyProfile,
	}}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/models?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/models: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var rows []event.ModelReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal models: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("models = %+v, want one row", rows)
	}
	if !rows[0].CostKnown || rows[0].Cost != 0 {
		t.Fatalf("zero-token request-only row cost = %.6f known=%v, want known zero cost", rows[0].Cost, rows[0].CostKnown)
	}

	req = httptest.NewRequest("GET", "/metering/api/overview?range=24h", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/overview: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var overview event.OverviewReport
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatalf("unmarshal overview: %v", err)
	}
	costData, ok := overview.Cost.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("cost data = %T, want map", overview.Cost.Data)
	}
	if partial, _ := costData["partial"].(bool); partial {
		t.Fatalf("overview cost partial = true for zero-token request-only row: %+v", costData)
	}
}

func TestAPIImagesSummary(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	s.pricing.Multimodal["gpt-image-2"] = pricing.MultimodalModelPrice{
		Text:  pricing.ModalityPrice{InputPer1M: 5.00, CachedInputPer1M: 1.25},
		Image: pricing.ModalityPrice{InputPer1M: 8.00, CachedInputPer1M: 2.00, OutputPer1M: 30.00},
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.InsertBatch([]db.UsageRecord{{
		CreatedAt:       now.Add(-10 * time.Minute).Format(time.RFC3339),
		RequestID:       "req-image",
		Endpoint:        "/v1/images/generations",
		Method:          "POST",
		Status:          200,
		LatencyMs:       100,
		EndpointProfile: "openai_images_generations",
		CaptureMode:     event.CaptureUsageMetered,
		MeteringKind:    event.MeteringImageTokens,
		CaptureOutcome:  event.OutcomeCaptured,
		ModelReturned:   "gpt-image-2",
		UsageDimensions: []db.UsageDimensionRecord{
			{Modality: "image", Channel: "text", Metric: "tokens", Direction: "input", Unit: "token", Amount: 10},
			{Modality: "image", Channel: "text", Metric: "tokens", Direction: "cached_input", Unit: "token", Amount: 2},
			{Modality: "image", Channel: "image", Metric: "tokens", Direction: "input", Unit: "token", Amount: 40},
			{Modality: "image", Channel: "image", Metric: "tokens", Direction: "cached_input", Unit: "token", Amount: 4},
			{Modality: "image", Channel: "image", Metric: "tokens", Direction: "output", Unit: "token", Amount: 50},
		},
		ImageUsage: &db.ImageUsageRecord{Operation: "generation", ModelReturned: "gpt-image-2", ImageCount: 1},
	}}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/images/summary?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/images/summary: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Summary db.ImageSummaryRow `json:"summary"`
		Cost    float64            `json:"cost"`
		Known   bool               `json:"cost_known"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal images summary: %v", err)
	}
	if payload.Summary.RequestCount != 1 || payload.Summary.TotalTokens != 100 || !payload.Known || payload.Cost <= 0 {
		t.Fatalf("payload = %+v", payload)
	}
	expected := 8/1_000_000.0*5.00 + 2/1_000_000.0*1.25 + 36/1_000_000.0*8.00 + 4/1_000_000.0*2.00 + 50/1_000_000.0*30.00
	if diff := payload.Cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("cost = %.6f, want %.6f", payload.Cost, expected)
	}

	summaryReq := httptest.NewRequest("GET", "/metering/api/summary?range=24h", nil)
	summaryRec := httptest.NewRecorder()
	s.ServeHTTP(summaryRec, summaryReq)
	if summaryRec.Code != 200 {
		t.Fatalf("GET /metering/api/summary: status %d, want 200; body=%s", summaryRec.Code, summaryRec.Body.String())
	}
	var summary event.SummaryReport
	if err := json.Unmarshal(summaryRec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if diff := summary.TotalCost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("summary total_cost = %.6f, want image cost %.6f", summary.TotalCost, expected)
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

func TestAPIOverviewAndIssuesAreRoutedNoStore(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.InsertBatch([]db.UsageRecord{
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/chat/completions", Method: "POST", Status: 429,
			LatencyMs: 200, TTFBMs: 40, ModelRequested: "gpt-4o",
			ErrorClass: "quota_exhausted", ErrorMessage: "Quota exhausted",
		},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/overview?range=7d", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("overview HTTP %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
		t.Fatalf("overview Cache-Control = %q, want no-store", got)
	}
	var overview event.OverviewReport
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatalf("unmarshal overview: %v", err)
	}
	if overview.Range != "7d" || overview.Recent1h.Data == nil {
		t.Fatalf("overview = %+v, want range=7d with recent_1h data", overview)
	}

	req = httptest.NewRequest("GET", "/metering/api/issues?range=24h", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("issues HTTP %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
		t.Fatalf("issues Cache-Control = %q, want no-store", got)
	}
	var issues event.IssuesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &issues); err != nil {
		t.Fatalf("unmarshal issues: %v", err)
	}
	if issues.Total != 1 || len(issues.Items) != 1 || issues.Items[0].Class != "quota_exhausted" {
		t.Fatalf("issues = %+v, want quota_exhausted item", issues)
	}
}

type fakeQuotaPoller struct {
	rows         []db.QuotaCurrentRow
	lastAt       time.Time
	apiAvailable bool
}

func (p *fakeQuotaPoller) Snapshot() ([]db.QuotaCurrentRow, time.Time, bool) {
	rows := make([]db.QuotaCurrentRow, len(p.rows))
	copy(rows, p.rows)
	return rows, p.lastAt, p.apiAvailable
}

func (p *fakeQuotaPoller) APICallAvailable() bool { return p.apiAvailable }
func (p *fakeQuotaPoller) Refresh()               {}

type fakeCredPoller struct {
	rows   []db.CredentialHealthRow
	lastAt time.Time
}

func (p *fakeCredPoller) Snapshot() ([]db.CredentialHealthRow, time.Time) {
	rows := make([]db.CredentialHealthRow, len(p.rows))
	copy(rows, p.rows)
	return rows, p.lastAt
}

func (p *fakeCredPoller) Refresh() {}

func (p *fakeCredPoller) ResetCooldown() error { return nil }

func TestAPIQuotaDoesNotClaimFullQuotaForProbeOnlyAvailability(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	s.SetQuotaPoller(&fakeQuotaPoller{lastAt: now, apiAvailable: true})

	req := httptest.NewRequest("GET", "/metering/api/quota", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/quota: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal quota: %v", err)
	}
	if payload["full_quota_available"] == true {
		t.Fatalf("quota payload claimed full quota without provider rows: %+v", payload)
	}
	if payload["module_status"] != "partial" || payload["phase"] != "credential_health" {
		t.Fatalf("quota payload = %+v, want partial credential_health", payload)
	}
}

func TestAPIQuotaClaimsFullQuotaOnlyWithSupportedRows(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	s.SetQuotaPoller(&fakeQuotaPoller{
		lastAt:       now,
		apiAvailable: true,
		rows: []db.QuotaCurrentRow{{
			Provider:        "codex",
			CredentialHash:  "cred",
			WindowKey:       "daily",
			CheckedAt:       now.Format(time.RFC3339),
			CheckedAtUnix:   now.Unix(),
			LimitAmount:     100,
			RemainingAmount: 80,
			Unit:            "requests",
			Status:          "ok",
			QuotaSupported:  1,
			AdapterStatus:   "available",
		}},
	})

	req := httptest.NewRequest("GET", "/metering/api/quota", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/quota: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal quota: %v", err)
	}
	if payload["full_quota_available"] != true || payload["module_status"] != "available" || payload["phase"] != "quota_snapshot" {
		t.Fatalf("quota payload = %+v, want available quota_snapshot", payload)
	}
}

func TestAPIQuotaFallsBackToCredentialRowsWhenQuotaRowsUnsupported(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	s.SetCredPoller(&fakeCredPoller{
		lastAt: now,
		rows: []db.CredentialHealthRow{{
			Provider:       "codex",
			CredentialHash: "cred",
			Status:         "warning",
			CheckedAt:      now.Format(time.RFC3339),
			CheckedAtUnix:  now.Unix(),
			ErrorClass:     "credential_history_warning",
		}},
	})
	s.SetQuotaPoller(&fakeQuotaPoller{
		lastAt:       now,
		apiAvailable: true,
		rows: []db.QuotaCurrentRow{{
			Provider:       "codex",
			CredentialHash: "unsupported",
			WindowKey:      "default",
			Status:         "unsupported",
			QuotaSupported: 0,
			AdapterStatus:  "unsupported",
			ErrorClass:     "quota_unsupported",
		}},
	})

	req := httptest.NewRequest("GET", "/metering/api/quota", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/quota: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal quota: %v", err)
	}
	if payload["module_status"] != "unsupported" || payload["phase"] != "credential_health" || payload["full_quota_available"] == true {
		t.Fatalf("quota payload = %+v, want unsupported credential_health fallback", payload)
	}
	items := payload["items"].([]any)
	item := items[0].(map[string]any)
	if item["credential_hash"] != "cred" {
		t.Fatalf("items = %+v, want credential health rows rather than unsupported quota rows", items)
	}
}

func TestAPIObservabilityReportsQuotaModuleStatus(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.InsertQuotaRefreshEvent(&db.QuotaRefreshEventRow{
		CheckedAt:     now.Format(time.RFC3339),
		CheckedAtUnix: now.Unix(),
		Provider:      "codex",
		Phase:         "probe",
		Status:        "error",
		AdapterStatus: "api_call_unavailable",
		ErrorClass:    "api_call_unavailable",
	}); err != nil {
		t.Fatalf("InsertQuotaRefreshEvent: %v", err)
	}
	s.SetQuotaPoller(&fakeQuotaPoller{lastAt: now, apiAvailable: true})

	req := httptest.NewRequest("GET", "/metering/api/observability", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/observability: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal observability: %v", err)
	}
	quota, ok := payload["quota"].(map[string]any)
	if !ok {
		t.Fatalf("observability quota = %T, want object: %+v", payload["quota"], payload)
	}
	if quota["module_status"] != "partial" || quota["full_quota_available"] == true || quota["phase"] != "credential_health" {
		t.Fatalf("observability quota = %+v, want partial credential_health without full quota", quota)
	}
	if quota["last_error"] != "api_call_unavailable" || quota["latest_event"] == nil {
		t.Fatalf("observability quota diagnostics = %+v, want latest api_call_unavailable", quota)
	}
}

func TestAPIQuotaIncludesRefreshDiagnostics(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.InsertQuotaRefreshEvent(&db.QuotaRefreshEventRow{
		CheckedAt:     now.Format(time.RFC3339),
		CheckedAtUnix: now.Unix(),
		Provider:      "codex",
		Phase:         "adapter",
		Status:        "error",
		AdapterStatus: "unsupported",
		ErrorClass:    "quota_unsupported",
	}); err != nil {
		t.Fatalf("InsertQuotaRefreshEvent: %v", err)
	}
	s.SetQuotaPoller(&fakeQuotaPoller{lastAt: now, apiAvailable: true})

	req := httptest.NewRequest("GET", "/metering/api/quota", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/quota: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal quota: %v", err)
	}
	diagnostics, ok := payload["diagnostics"].([]any)
	if !ok || len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one refresh event", payload["diagnostics"])
	}
	first := diagnostics[0].(map[string]any)
	if first["error_class"] != "quota_unsupported" || first["provider"] != "codex" {
		t.Fatalf("diagnostic = %+v, want safe quota refresh fields", first)
	}
}

func TestAPIQuotaDiagnosticsEndpoint(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	s.SetCredPoller(&fakeCredPoller{
		lastAt: now,
		rows: []db.CredentialHealthRow{{
			Provider:       "codex",
			CredentialHash: "cred",
			DisplayLabel:   "Codex Primary",
			IdentityHint:   "codex@example.com",
			Status:         "warning",
			CheckedAt:      now.Format(time.RFC3339),
			CheckedAtUnix:  now.Unix(),
			ErrorClass:     "credential_history_warning",
		}},
	})
	s.SetQuotaPoller(&fakeQuotaPoller{lastAt: now, apiAvailable: true})
	if err := database.InsertQuotaRefreshEvent(&db.QuotaRefreshEventRow{
		CheckedAt:        now.Format(time.RFC3339),
		CheckedAtUnix:    now.Unix(),
		Provider:         "codex",
		Phase:            "adapter",
		Status:           "error",
		AdapterStatus:    "unsupported",
		ErrorClass:       "quota_unsupported",
		ProbeHTTPStatus:  400,
		ProbeEndpoint:    "/api-call",
		ProbeErrorClass:  "api_call_bad_request",
		APICallReachable: 1,
	}); err != nil {
		t.Fatalf("InsertQuotaRefreshEvent: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/quota/diagnostics", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/quota/diagnostics: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal diagnostics: %v", err)
	}
	if payload["api_call_available"] != true || payload["credential_health_enabled"] != true || payload["quota_enabled"] != true {
		t.Fatalf("diagnostics flags = %+v", payload)
	}
	credentials := payload["credentials"].(map[string]any)
	if credentials["total"].(float64) != 1 || credentials["warning"].(float64) != 1 {
		t.Fatalf("credential summary = %+v", credentials)
	}
	events := payload["events"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["probe_error_class"] != "api_call_bad_request" {
		t.Fatalf("events = %+v, want probe diagnostics", events)
	}
}

func TestAPIQuotaDiagnosticsDoesNotExposeZeroTime(t *testing.T) {
	s, _ := newTestServer(t, "/metering")

	req := httptest.NewRequest("GET", "/metering/api/quota/diagnostics", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/quota/diagnostics: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal diagnostics: %v", err)
	}
	if payload["checked_at"] != "" {
		t.Fatalf("checked_at = %q, want empty before any poller check", payload["checked_at"])
	}
}

func TestAPIObservabilityReportsCredentialHealthFallback(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	s.SetCredPoller(&fakeCredPoller{
		lastAt: now,
		rows: []db.CredentialHealthRow{{
			Provider:       "codex",
			CredentialHash: "cred",
			Status:         "warning",
			CheckedAt:      now.Format(time.RFC3339),
			CheckedAtUnix:  now.Unix(),
			ErrorClass:     "credential_history_warning",
		}},
	})
	s.SetQuotaPoller(&fakeQuotaPoller{lastAt: now, apiAvailable: false})

	req := httptest.NewRequest("GET", "/metering/api/observability", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/observability: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal observability: %v", err)
	}
	quota := payload["quota"].(map[string]any)
	if quota["module_status"] != "partial" || quota["credential_fallback"] != true || quota["full_quota_available"] == true {
		t.Fatalf("observability quota = %+v, want partial credential fallback without full quota", quota)
	}
	credential := payload["credential_health"].(map[string]any)
	if credential["warning_count"] != float64(1) || credential["error_count"] != float64(0) {
		t.Fatalf("credential health = %+v, want one warning and no errors", credential)
	}
}

func TestAPIObservabilityReportsQuotaUnavailableWithoutProbeOrRows(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)
	s.SetQuotaPoller(&fakeQuotaPoller{lastAt: now, apiAvailable: false})

	req := httptest.NewRequest("GET", "/metering/api/observability", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/observability: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal observability: %v", err)
	}
	quota, ok := payload["quota"].(map[string]any)
	if !ok {
		t.Fatalf("observability quota = %T, want object: %+v", payload["quota"], payload)
	}
	if quota["module_status"] != "unavailable" || quota["full_quota_available"] == true {
		t.Fatalf("observability quota = %+v, want unavailable without probe or rows", quota)
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
		{CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 500, LatencyMs: 300, TTFBMs: 60, CaptureOutcome: "failed", Error: "upstream", ErrorClass: "upstream_5xx", ErrorCode: "internal_error"},
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
	if data.LatestErrorClass != "upstream_5xx" || data.LatestErrorCode != "internal_error" {
		t.Fatalf("activity latest diagnostic = class %q code %q, want upstream_5xx/internal_error", data.LatestErrorClass, data.LatestErrorCode)
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

func TestGatewayCapabilities(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC().Truncate(time.Second)

	// Insert traffic covering the four capture-mode distinctions:
	//   - usage_metered with captured outcome (healthy)
	//   - usage_metered with skipped outcome (missing usage)
	//   - request_only (audio)
	//   - passthrough (unknown route)
	// Plus a streaming usage_metered request.
	records := []db.UsageRecord{
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/chat/completions", Method: "POST", Status: 200,
			EndpointProfile: "chat_completions", CaptureMode: "usage_metered",
			MeteringKind: "llm_tokens", CaptureOutcome: "captured",
			ModelReturned: "gpt-4o", InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		},
		{
			CreatedAt: now.Add(-8 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/chat/completions", Method: "POST", Status: 200,
			Stream:          true,
			EndpointProfile: "chat_completions", CaptureMode: "usage_metered",
			MeteringKind: "llm_tokens", CaptureOutcome: "captured",
			ModelReturned: "gpt-4o", InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		},
		{
			CreatedAt: now.Add(-6 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/responses", Method: "POST", Status: 200,
			EndpointProfile: "responses", CaptureMode: "usage_metered",
			MeteringKind: "llm_tokens", CaptureOutcome: "skipped",
			CaptureReason: "usage_not_present",
		},
		{
			CreatedAt: now.Add(-4 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/audio/transcriptions", Method: "POST", Status: 200,
			EndpointProfile: "openai_audio", CaptureMode: "request_only",
			MeteringKind: "request_only", CaptureOutcome: "captured",
			CaptureReason: "request_only_profile",
		},
		{
			CreatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/some-unknown-endpoint", Method: "POST", Status: 200,
			EndpointProfile: "unknown_passthrough", CaptureMode: "passthrough",
			MeteringKind: "none", CaptureOutcome: "captured",
		},
	}
	if err := database.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/gateway/capabilities?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /metering/api/gateway/capabilities: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var report event.GatewayCapabilitiesReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v; body=%s", err, rec.Body.String())
	}

	if report.Range != "24h" {
		t.Errorf("Range = %q, want 24h", report.Range)
	}

	// Summary: 5 total, 3 usage_metered, 1 request_only, 1 passthrough,
	// 1 stream, 1 missing usage.
	if report.Summary.TotalRequests != 5 {
		t.Errorf("TotalRequests = %d, want 5", report.Summary.TotalRequests)
	}
	if report.Summary.UsageMeteredReqs != 3 {
		t.Errorf("UsageMeteredReqs = %d, want 3", report.Summary.UsageMeteredReqs)
	}
	if report.Summary.RequestOnlyReqs != 1 {
		t.Errorf("RequestOnlyReqs = %d, want 1", report.Summary.RequestOnlyReqs)
	}
	if report.Summary.PassthroughReqs != 1 {
		t.Errorf("PassthroughReqs = %d, want 1", report.Summary.PassthroughReqs)
	}
	if report.Summary.StreamRequests != 1 {
		t.Errorf("StreamRequests = %d, want 1", report.Summary.StreamRequests)
	}
	if report.Summary.MissingUsageReqs != 1 {
		t.Errorf("MissingUsageReqs = %d, want 1", report.Summary.MissingUsageReqs)
	}

	// Find specific profiles and verify counts.
	byName := make(map[string]event.GatewayCapabilityProfile, len(report.Profiles))
	for _, p := range report.Profiles {
		byName[p.Name] = p
	}

	chat, ok := byName["chat_completions"]
	if !ok {
		t.Fatal("missing chat_completions profile in response")
	}
	if chat.CaptureMode != "usage_metered" {
		t.Errorf("chat_completions CaptureMode = %q, want usage_metered", chat.CaptureMode)
	}
	if chat.RequestCount != 2 {
		t.Errorf("chat_completions RequestCount = %d, want 2", chat.RequestCount)
	}
	if chat.StreamCount != 1 {
		t.Errorf("chat_completions StreamCount = %d, want 1", chat.StreamCount)
	}
	if chat.MissingUsageCount != 0 {
		t.Errorf("chat_completions MissingUsageCount = %d, want 0", chat.MissingUsageCount)
	}

	responses, ok := byName["responses"]
	if !ok {
		t.Fatal("missing responses profile in response")
	}
	if responses.MissingUsageCount != 1 {
		t.Errorf("responses MissingUsageCount = %d, want 1", responses.MissingUsageCount)
	}

	audio, ok := byName["openai_audio"]
	if !ok {
		t.Fatal("missing openai_audio profile in response")
	}
	if audio.CaptureMode != "request_only" {
		t.Errorf("openai_audio CaptureMode = %q, want request_only", audio.CaptureMode)
	}
	if audio.RequestCount != 1 {
		t.Errorf("openai_audio RequestCount = %d, want 1", audio.RequestCount)
	}

	// Unknown passthrough should be present and not rendered as an error.
	passthrough, ok := byName["unknown_passthrough"]
	if !ok {
		t.Fatal("missing unknown_passthrough profile in response")
	}
	if passthrough.CaptureMode != "passthrough" {
		t.Errorf("unknown_passthrough CaptureMode = %q, want passthrough", passthrough.CaptureMode)
	}
	if passthrough.RequestCount != 1 {
		t.Errorf("unknown_passthrough RequestCount = %d, want 1", passthrough.RequestCount)
	}

	// Profiles with SSE support should list the compressed_sse_not_metered limitation.
	if chat.CaptureMode == "usage_metered" {
		found := false
		for _, lim := range chat.KnownLimitations {
			if lim == "compressed_sse_not_metered" {
				found = true
			}
		}
		if !found {
			t.Errorf("chat_completions KnownLimitations = %v, want compressed_sse_not_metered", chat.KnownLimitations)
		}
	}

	// All registry profiles should be listed even with zero traffic.
	embeddings, ok := byName["openai_embeddings"]
	if !ok {
		t.Fatal("missing openai_embeddings profile (zero-traffic profile should still be listed)")
	}
	if embeddings.RequestCount != 0 {
		t.Errorf("openai_embeddings RequestCount = %d, want 0", embeddings.RequestCount)
	}
}

func TestGatewayCapabilitiesEmptyDB(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	req := httptest.NewRequest("GET", "/metering/api/gateway/capabilities?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	var report event.GatewayCapabilitiesReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// Empty DB should still list all registry profiles with zero counts.
	if len(report.Profiles) == 0 {
		t.Error("expected profiles from registry even with empty DB")
	}
	if report.Summary.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0", report.Summary.TotalRequests)
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

func TestAPIRequestsErrorClassFilter(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	now := time.Now().UTC()
	records := []db.UsageRecord{
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 429, ErrorClass: "quota_exhausted", ErrorMessage: "quota"},
		{CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 401, ErrorClass: "auth_failed", ErrorMessage: "auth"},
	}
	if err := database.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/requests?range=24h&error_class=quota_exhausted", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("HTTP %d, want 200", rec.Code)
	}
	var rows []event.RequestReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 1 || rows[0].ErrorClass != "quota_exhausted" {
		t.Fatalf("rows = %+v, want one quota_exhausted row", rows)
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
	if resp.BucketCount != 2 || resp.NonzeroBucketCount != 1 {
		t.Fatalf("health bucket counts = %+v, want 2/1 (no baseline)", resp)
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
	if len(resp.Timeline) != 1 || resp.BucketCount != 3 || resp.NonzeroBucketCount != 1 {
		t.Fatalf("nonzero health response = %+v, want request error only (health delta=0, no baseline)", resp)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestCPAAuthAndProviderQuotaEndpoints(t *testing.T) {
	s, _ := newTestServer(t, "/metering")

	// Register pollers with fake data.
	s.SetCredPoller(&fakeCredPoller{
		rows:   []db.CredentialHealthRow{{Provider: "codex", Status: "ready"}},
		lastAt: time.Now(),
	})
	s.SetQuotaPoller(&fakeQuotaPoller{
		rows:         []db.QuotaCurrentRow{{Provider: "claude", Status: "ok", QuotaSupported: 1}},
		lastAt:       time.Now(),
		apiAvailable: true,
	})

	// GET /api/cpa/auth returns cached data, no CPA call.
	req := httptest.NewRequest("GET", "/metering/api/cpa/auth", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /api/cpa/auth: status %d", rec.Code)
	}
	var authResp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &authResp)
	if authResp["enabled"] != true {
		t.Error("authResp enabled should be true")
	}
	items := authResp["items"].([]any)
	if len(items) != 1 {
		t.Errorf("items len = %d, want 1", len(items))
	}

	// POST /api/cpa/auth/refresh triggers Refresh.
	req = httptest.NewRequest("POST", "/metering/api/cpa/auth/refresh", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST /api/cpa/auth/refresh: status %d", rec.Code)
	}

	// GET /api/provider-quota returns cached data.
	req = httptest.NewRequest("GET", "/metering/api/provider-quota", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /api/provider-quota: status %d", rec.Code)
	}
	var quotaResp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &quotaResp)
	if quotaResp["module_status"] != "available" {
		t.Errorf("module_status = %v, want available", quotaResp["module_status"])
	}

	// POST /api/provider-quota/refresh triggers Refresh.
	req = httptest.NewRequest("POST", "/metering/api/provider-quota/refresh", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST /api/provider-quota/refresh: status %d", rec.Code)
	}

	// POST /api/cpa/cooldown/reset calls ResetCooldown.
	req = httptest.NewRequest("POST", "/metering/api/cpa/cooldown/reset", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST /api/cpa/cooldown/reset: status %d", rec.Code)
	}
}

func TestProviderQuotaUnsupportedWhenNoAdapterRows(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	s.SetQuotaPoller(&fakeQuotaPoller{
		rows:         []db.QuotaCurrentRow{{Provider: "claude", Status: "unsupported", QuotaSupported: 0}},
		lastAt:       time.Now(),
		apiAvailable: true,
	})

	req := httptest.NewRequest("GET", "/metering/api/provider-quota", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["module_status"] != "unsupported" {
		t.Errorf("module_status = %v, want unsupported", resp["module_status"])
	}
}

func TestModelAssets(t *testing.T) {
	s, database := newTestServer(t, "/metering")
	s.pricing.Models["gpt-4o"] = pricing.ModelPrice{InputPer1M: 2.5, OutputPer1M: 10.0}
	now := time.Now().UTC().Truncate(time.Second)

	records := []db.UsageRecord{
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/chat/completions", Method: "POST", Status: 200,
			EndpointProfile: "chat_completions", CaptureMode: "usage_metered",
			ModelRequested: "gpt-4o", ModelReturned: "gpt-4o",
			InputTokens: 100, OutputTokens: 50, TotalTokens: 150,
		},
		{
			CreatedAt: now.Add(-8 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/chat/completions", Method: "POST", Status: 500,
			EndpointProfile: "chat_completions", CaptureMode: "usage_metered",
			ModelRequested: "gpt-4o", ModelReturned: "gpt-4o",
			InputTokens: 50, OutputTokens: 20, TotalTokens: 70,
		},
		{
			CreatedAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/audio/transcriptions", Method: "POST", Status: 200,
			EndpointProfile: "openai_audio", CaptureMode: "request_only",
			ModelRequested: "whisper-1",
		},
		{
			CreatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/chat/completions", Method: "POST", Status: 200,
			EndpointProfile: "chat_completions", CaptureMode: "usage_metered",
			ModelRequested: "unpriced-model", ModelReturned: "unpriced-model",
			InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		},
	}
	if err := database.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	req := httptest.NewRequest("GET", "/metering/api/model-assets?range=24h", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}

	var report event.ModelAssetsReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	byModel := make(map[string]event.ModelAssetItem, len(report.Items))
	for _, item := range report.Items {
		byModel[item.Model] = item
	}

	gpt, ok := byModel["gpt-4o"]
	if !ok {
		t.Fatal("missing gpt-4o")
	}
	if gpt.RequestCount != 2 {
		t.Errorf("gpt-4o request_count = %d, want 2", gpt.RequestCount)
	}
	if gpt.FailedCount != 1 {
		t.Errorf("gpt-4o failed_count = %d, want 1", gpt.FailedCount)
	}
	if !gpt.CostKnown {
		t.Error("gpt-4o should be priced (cost_known=true)")
	}
	if gpt.PricingSource != "exact" {
		t.Errorf("gpt-4o pricing_source = %q, want exact", gpt.PricingSource)
	}
	if gpt.EstimatedCost <= 0 {
		t.Errorf("gpt-4o estimated_cost = %v, want > 0", gpt.EstimatedCost)
	}

	unpriced, ok := byModel["unpriced-model"]
	if !ok {
		t.Fatal("missing unpriced-model")
	}
	if unpriced.CostKnown {
		t.Error("unpriced-model should not be priced")
	}

	whisper, ok := byModel["whisper-1"]
	if !ok {
		t.Fatal("missing whisper-1")
	}
	if whisper.CaptureMode != "request_only" {
		t.Errorf("whisper-1 capture_mode = %q, want request_only", whisper.CaptureMode)
	}

	if report.Summary.UsedModels != 3 {
		t.Errorf("summary used_models = %d, want 3", report.Summary.UsedModels)
	}
	if report.Summary.UnpricedUsedModels != 2 {
		t.Errorf("summary unpriced_used_models = %d, want 2 (whisper-1 + unpriced-model)", report.Summary.UnpricedUsedModels)
	}
	if report.Summary.RequestOnlyModels != 1 {
		t.Errorf("summary request_only_models = %d, want 1", report.Summary.RequestOnlyModels)
	}
}
