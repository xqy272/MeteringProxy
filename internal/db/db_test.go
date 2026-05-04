package db

import (
	"database/sql"
	"os"
	"runtime"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	path := t.TempDir() + "/test.sqlite"
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func insertRecord(t *testing.T, d *DB, createdAt string, endpoint string, status int, model string, input, output, total int64) {
	t.Helper()
	records := []UsageRecord{{
		CreatedAt:     createdAt,
		Endpoint:      endpoint,
		Method:        "POST",
		Status:        status,
		LatencyMs:     100,
		Stream:        true,
		ModelReturned: model,
		InputTokens:   input,
		OutputTokens:  output,
		TotalTokens:   total,
	}}
	if err := d.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
}

// ts returns an RFC3339 timestamp offset from now.
func ts(d time.Duration) string {
	return time.Now().Add(d).Format(time.RFC3339)
}

func TestOpenAndMigrate(t *testing.T) {
	d := newTestDB(t)
	if d.read == nil {
		t.Fatal("read connection is nil")
	}
	if d.read == d.sql {
		t.Fatal("read and write connections should be independent")
	}
	var count int
	err := d.sql.QueryRow("SELECT COUNT(*) FROM request_usage").Scan(&count)
	if err != nil {
		t.Fatalf("query request_usage: %v", err)
	}
	err = d.sql.QueryRow("SELECT COUNT(*) FROM health_metrics").Scan(&count)
	if err != nil {
		t.Fatalf("query health_metrics: %v", err)
	}
	var userVersion int
	if err := d.sql.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	if userVersion != schemaVersion {
		t.Errorf("user_version = %d, want %d", userVersion, schemaVersion)
	}
}

func TestMigrateAddsMissingColumnsToLegacyDB(t *testing.T) {
	path := t.TempDir() + "/legacy.sqlite"
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	_, err = raw.Exec(`
		CREATE TABLE request_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			method TEXT NOT NULL,
			status INTEGER NOT NULL,
			latency_ms INTEGER NOT NULL,
			stream INTEGER NOT NULL
		)
	`)
	if err != nil {
		raw.Close()
		t.Fatalf("create legacy table: %v", err)
	}
	raw.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open legacy DB: %v", err)
	}
	defer d.Close()

	if err := d.InsertBatch([]UsageRecord{{
		CreatedAt:      ts(-1 * time.Minute),
		Endpoint:       "/v1/responses",
		Method:         "POST",
		Status:         200,
		LatencyMs:      42,
		TTFBMs:         7,
		Stream:         false,
		APIKeyHash:     "hash",
		ModelReturned:  "gpt-5.4-mini",
		InputTokens:    3,
		OutputTokens:   4,
		TotalTokens:    7,
		RequestBytes:   100,
		ResponseBytes:  200,
		RequestID:      "req_legacy",
		ClientIPHash:   "iphash",
		ModelRequested: "gpt-5.4-mini",
	}}); err != nil {
		t.Fatalf("InsertBatch after legacy migration: %v", err)
	}

	if err := d.InsertHealthMetric(ts(0), 1, 2, 3, 4, 0); err != nil {
		t.Fatalf("InsertHealthMetric after legacy migration: %v", err)
	}

	rows, err := d.Requests(10, 0, 0, "", "", "", time.Time{})
	if err != nil {
		t.Fatalf("Requests after legacy migration: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "req_legacy" || rows[0].TTFBMs != 7 {
		t.Fatalf("unexpected migrated rows: %+v", rows)
	}
}

func TestInsertBatch(t *testing.T) {
	d := newTestDB(t)
	records := []UsageRecord{
		{CreatedAt: ts(-1 * time.Hour), Endpoint: "/v1/chat/completions", Method: "POST", Status: 200, LatencyMs: 150, Stream: true, ModelReturned: "gpt-4o", InputTokens: 100, OutputTokens: 200, TotalTokens: 300},
		{CreatedAt: ts(-30 * time.Minute), Endpoint: "/v1/responses", Method: "POST", Status: 200, LatencyMs: 200, Stream: false, ModelReturned: "gpt-5.4-mini", InputTokens: 50, OutputTokens: 60, TotalTokens: 110},
	}
	if err := d.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	var count int
	d.sql.QueryRow("SELECT COUNT(*) FROM request_usage").Scan(&count)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestSummary(t *testing.T) {
	d := newTestDB(t)
	insertRecord(t, d, ts(-1*time.Hour), "/v1/chat/completions", 200, "gpt-4o", 100, 200, 300)
	insertRecord(t, d, ts(-30*time.Minute), "/v1/chat/completions", 500, "gpt-4o", 10, 20, 30)

	since := time.Now().Add(-24 * time.Hour)
	row, err := d.Summary(since)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if row.TotalRequests != 2 {
		t.Errorf("total = %d, want 2", row.TotalRequests)
	}
	if row.FailedRequests != 1 {
		t.Errorf("failed = %d, want 1", row.FailedRequests)
	}
	if row.TotalInputTokens != 110 {
		t.Errorf("input_tokens = %d, want 110", row.TotalInputTokens)
	}
}

func TestModels(t *testing.T) {
	d := newTestDB(t)
	insertRecord(t, d, ts(-2*time.Hour), "/v1/chat/completions", 200, "gpt-4o", 100, 200, 300)
	insertRecord(t, d, ts(-1*time.Hour), "/v1/chat/completions", 200, "gpt-4o", 50, 60, 110)
	insertRecord(t, d, ts(-30*time.Minute), "/v1/chat/completions", 200, "deepseek-chat", 30, 40, 70)

	rows, err := d.Models(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 models, got %d", len(rows))
	}
	if rows[0].Model != "gpt-4o" || rows[0].RequestCount != 2 {
		t.Errorf("first model: %s (count=%d), want gpt-4o (count=2)", rows[0].Model, rows[0].RequestCount)
	}
	if rows[1].Model != "deepseek-chat" || rows[1].RequestCount != 1 {
		t.Errorf("second model: %s (count=%d), want deepseek-chat (count=1)", rows[1].Model, rows[1].RequestCount)
	}
}

func TestTokenAggregatesIncludeCacheCreation(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.InsertBatch([]UsageRecord{
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

	models, err := d.Models(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0].CacheCreationTokens != 5 {
		t.Fatalf("models = %+v, want cache_creation_tokens=5", models)
	}

	buckets, err := d.ModelTimeseries(now.Add(-time.Hour), 60)
	if err != nil {
		t.Fatalf("ModelTimeseries: %v", err)
	}
	if len(buckets) != 1 || buckets[0].CacheCreationTokens != 5 {
		t.Fatalf("model timeseries = %+v, want cache_creation_tokens=5", buckets)
	}
}

func TestModelsAndKeysTreatEmptyAsUnknown(t *testing.T) {
	d := newTestDB(t)
	insertRecord(t, d, ts(-1*time.Minute), "/v1/chat/completions", 200, "", 1, 2, 3)

	models, err := d.Models(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0].Model != "unidentified" {
		t.Fatalf("models = %+v, want one unidentified row", models)
	}

	keys, err := d.Keys(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 || keys[0].KeyHash != "unknown" {
		t.Fatalf("keys = %+v, want one unknown row", keys)
	}
}

func TestRequestsModelFilterUsesEffectiveModel(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.InsertBatch([]UsageRecord{
		{CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/chat/completions", Method: "POST", Status: 429, LatencyMs: 10, ModelRequested: "gpt-4o"},
		{CreatedAt: now.Add(-9 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/chat/completions", Method: "POST", Status: 200, LatencyMs: 10, ModelReturned: "gpt-4o"},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	rows, err := d.Requests(10, 0, 0, "gpt-4o", "", "", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Requests: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want both returned and requested model rows", rows)
	}
}

func TestRequestsEndpointFilterSupportsProfiles(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.InsertBatch([]UsageRecord{
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1beta/models/gemini-2.5-pro:generateContent", EndpointProfile: "gemini_generate_content",
			Method: "POST", Status: 200, LatencyMs: 10, ModelReturned: "gemini-2.5-pro",
		},
		{
			CreatedAt: now.Add(-9 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1beta/models/gemini-2.5-flash:streamGenerateContent", EndpointProfile: "gemini_generate_content",
			Method: "POST", Status: 200, LatencyMs: 10, ModelReturned: "gemini-2.5-flash",
		},
		{
			CreatedAt: now.Add(-8 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/messages", EndpointProfile: "anthropic_messages",
			Method: "POST", Status: 200, LatencyMs: 10, ModelReturned: "claude-sonnet-4-6",
		},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	rows, err := d.Requests(10, 0, 0, "", "profile:gemini_generate_content", "", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Requests profile filter: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two Gemini profile rows", rows)
	}

	rows, err = d.Requests(10, 0, 0, "", "/v1/messages", "", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Requests endpoint filter: %v", err)
	}
	if len(rows) != 1 || rows[0].EndpointProfile != "anthropic_messages" {
		t.Fatalf("rows = %+v, want one Anthropic row", rows)
	}
}

func TestIssuesAggregatesByEffectiveModelAndUsesLatestSample(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.InsertBatch([]UsageRecord{
		{
			CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/chat/completions", Method: "POST",
			Status: 429, LatencyMs: 10, APIKeyHash: "key-a", ModelRequested: "gpt-4o",
			ErrorClass: "rate_limited", ErrorCode: "old", ErrorMessage: "old sample", RequestID: "req-old",
		},
		{
			CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/chat/completions", Method: "POST",
			Status: 429, LatencyMs: 10, APIKeyHash: "key-a", ModelRequested: "gpt-4o",
			ErrorClass: "rate_limited", ErrorCode: "new", ErrorMessage: "new sample", RequestID: "req-new",
		},
		{
			CreatedAt: now.Add(-25 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST",
			Status: 401, LatencyMs: 10, APIKeyHash: "key-b", ModelRequested: "gpt-5.4",
			ErrorClass: "auth_failed", ErrorMessage: "bad key", RequestID: "req-auth",
		},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	rows, err := d.Issues(now.Add(-time.Hour), 20)
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("issues = %+v, want 2 grouped rows", rows)
	}
	if rows[0].Class != "auth_failed" {
		t.Fatalf("first issue class = %q, want auth_failed sorted before warning", rows[0].Class)
	}
	if rows[1].Class != "rate_limited" || rows[1].Count != 2 || rows[1].Message != "new sample" || rows[1].RequestID != "req-new" {
		t.Fatalf("rate limit issue = %+v, want count=2 with latest sample", rows[1])
	}
	if rows[1].Model != "gpt-4o" || rows[1].ModelSource != "requested" {
		t.Fatalf("rate limit model attribution = %+v, want requested gpt-4o", rows[1])
	}
}

func TestSinceFilteringUsesTimestampEpoch(t *testing.T) {
	d := newTestDB(t)
	records := []UsageRecord{
		{
			CreatedAt:     "2026-05-02T00:30:00+14:00", // 2026-05-01T10:30:00Z, before since
			Endpoint:      "/v1/chat/completions",
			Method:        "POST",
			Status:        200,
			LatencyMs:     1,
			Stream:        false,
			ModelReturned: "old-offset-row",
			TotalTokens:   1,
		},
		{
			CreatedAt:     "2026-05-01T12:30:00Z",
			Endpoint:      "/v1/chat/completions",
			Method:        "POST",
			Status:        200,
			LatencyMs:     1,
			Stream:        false,
			ModelReturned: "included-row",
			TotalTokens:   2,
		},
	}
	if err := d.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	since, err := time.Parse(time.RFC3339, "2026-05-01T12:00:00Z")
	if err != nil {
		t.Fatalf("parse since: %v", err)
	}
	summary, err := d.Summary(since)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.TotalRequests != 1 || summary.TotalTokens != 2 {
		t.Fatalf("summary = %+v, want only the chronologically included row", summary)
	}
}

func TestRequests_StatusCategories(t *testing.T) {
	d := newTestDB(t)
	// Insert rows with various status codes covering 2xx, 3xx, 4xx, 5xx
	insertRecord(t, d, ts(-3*time.Hour), "/v1/chat/completions", 200, "gpt-4o", 1, 1, 2)
	insertRecord(t, d, ts(-3*time.Hour), "/v1/chat/completions", 201, "gpt-4o", 1, 1, 2)
	insertRecord(t, d, ts(-2*time.Hour), "/v1/chat/completions", 302, "gpt-4o", 0, 0, 0)
	insertRecord(t, d, ts(-2*time.Hour), "/v1/chat/completions", 400, "gpt-4o", 0, 0, 0)
	insertRecord(t, d, ts(-1*time.Hour), "/v1/chat/completions", 429, "gpt-4o", 0, 0, 0)
	insertRecord(t, d, ts(-30*time.Minute), "/v1/chat/completions", 500, "gpt-4o", 0, 0, 0)
	insertRecord(t, d, ts(-15*time.Minute), "/v1/chat/completions", 502, "gpt-4o", 0, 0, 0)

	all, err := d.Requests(100, 0, 0, "", "", "", time.Time{})
	if err != nil {
		t.Fatalf("Requests(all): %v", err)
	}
	if len(all) != 7 {
		t.Errorf("all: got %d rows, want 7", len(all))
	}

	success, err := d.Requests(100, 200, 300, "", "", "", time.Time{})
	if err != nil {
		t.Fatalf("Requests(success): %v", err)
	}
	if len(success) != 2 {
		t.Errorf("success (200-299): got %d rows, want 2", len(success))
	}
	for _, r := range success {
		if r.Status < 200 || r.Status > 299 {
			t.Errorf("success filter included status %d", r.Status)
		}
	}

	clientErrors, err := d.Requests(100, 400, 500, "", "", "", time.Time{})
	if err != nil {
		t.Fatalf("Requests(4xx): %v", err)
	}
	if len(clientErrors) != 2 {
		t.Errorf("4xx (400-499): got %d rows, want 2", len(clientErrors))
	}
	for _, r := range clientErrors {
		if r.Status < 400 || r.Status > 499 {
			t.Errorf("4xx filter included status %d", r.Status)
		}
	}

	serverErrors, err := d.Requests(100, 500, 0, "", "", "", time.Time{})
	if err != nil {
		t.Fatalf("Requests(5xx): %v", err)
	}
	if len(serverErrors) != 2 {
		t.Errorf("5xx (>=500): got %d rows, want 2", len(serverErrors))
	}
	for _, r := range serverErrors {
		if r.Status < 500 {
			t.Errorf("5xx filter included status %d", r.Status)
		}
	}
}

func TestRequests_RangeFilter(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	oldRecord := UsageRecord{
		CreatedAt:   now.Add(-48 * time.Hour).Format(time.RFC3339),
		Endpoint:    "/v1/chat/completions",
		Method:      "POST",
		Status:      200,
		LatencyMs:   100,
		Stream:      false,
		TotalTokens: 10,
	}
	recentRecord := UsageRecord{
		CreatedAt:   now.Add(-1 * time.Hour).Format(time.RFC3339),
		Endpoint:    "/v1/chat/completions",
		Method:      "POST",
		Status:      200,
		LatencyMs:   50,
		Stream:      true,
		TotalTokens: 20,
	}
	if err := d.InsertBatch([]UsageRecord{oldRecord, recentRecord}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	since24h := now.Add(-24 * time.Hour)
	rows, err := d.Requests(100, 0, 0, "", "", "", since24h)
	if err != nil {
		t.Fatalf("Requests with 24h range: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("24h range: got %d rows, want 1 (only recent)", len(rows))
	}
	if len(rows) > 0 && rows[0].TotalTokens != 20 {
		t.Errorf("24h range: got tokens %d, want 20", rows[0].TotalTokens)
	}

	since7d := now.Add(-7 * 24 * time.Hour)
	rowsAll, err := d.Requests(100, 0, 0, "", "", "", since7d)
	if err != nil {
		t.Fatalf("Requests with 7d range: %v", err)
	}
	if len(rowsAll) != 2 {
		t.Errorf("7d range: got %d rows, want 2 (both records)", len(rowsAll))
	}

	rowsZeroSince, err := d.Requests(100, 0, 0, "", "", "", time.Time{})
	if err != nil {
		t.Fatalf("Requests with zero since: %v", err)
	}
	if len(rowsZeroSince) != 2 {
		t.Errorf("zero since (no filter): got %d rows, want 2", len(rowsZeroSince))
	}
}

func TestTimeseries(t *testing.T) {
	d := newTestDB(t)
	// Bucket to the current hour boundary for reliable 10-min bucket alignment.
	now := time.Now().UTC().Truncate(time.Hour)
	insertRecord(t, d, now.Add(5*time.Minute).Format(time.RFC3339), "/v1/chat/completions", 200, "gpt-4o", 10, 20, 30)
	insertRecord(t, d, now.Add(8*time.Minute).Format(time.RFC3339), "/v1/chat/completions", 200, "gpt-4o", 5, 10, 15)
	insertRecord(t, d, now.Add(15*time.Minute).Format(time.RFC3339), "/v1/chat/completions", 200, "gpt-4o", 2, 3, 5)

	rows, err := d.Timeseries(now.Add(-1*time.Hour), 10)
	if err != nil {
		t.Fatalf("Timeseries: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected non-empty timeseries")
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 buckets, got %d", len(rows))
	}
	if rows[0].Count != 2 {
		t.Errorf("bucket 1 count = %d, want 2", rows[0].Count)
	}
	if rows[0].TotalTokens != 45 {
		t.Errorf("bucket 1 total_tokens = %d, want 45", rows[0].TotalTokens)
	}
	if rows[0].FailedCount != 0 || rows[0].AvgLatencyMs != 100 {
		t.Errorf("bucket 1 failed/avg latency = %d/%d, want 0/100", rows[0].FailedCount, rows[0].AvgLatencyMs)
	}
	if rows[1].Count != 1 {
		t.Errorf("bucket 2 count = %d, want 1", rows[1].Count)
	}
}

func TestActivity(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	records := []UsageRecord{
		{CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 200, LatencyMs: 100, TTFBMs: 20, CaptureOutcome: "captured", ModelReturned: "gpt-5.4"},
		{CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/responses", Method: "POST", Status: 500, LatencyMs: 300, TTFBMs: 60, CaptureOutcome: "failed", CaptureReason: "parse_error", Error: "upstream failed", ModelReturned: "gpt-5.4"},
		{CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/chat/completions", Method: "POST", Status: 429, LatencyMs: 200, TTFBMs: 40, CaptureOutcome: "skipped", CaptureReason: "rate_limited", Error: "rate limited", ModelRequested: "gpt-4o"},
	}
	if err := d.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	row, err := d.Activity(now.Add(-1 * time.Hour))
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if row.SampleSize != 3 || row.SuccessCount != 1 || row.FailedCount != 2 {
		t.Fatalf("activity counts = %+v, want sample=3 success=1 failed=2", row)
	}
	if row.AvgLatencyMs != 200 || row.P95LatencyMs != 300 {
		t.Fatalf("latency = avg %d p95 %d, want 200/300", row.AvgLatencyMs, row.P95LatencyMs)
	}
	if row.AvgTTFBMs != 40 || row.P95TTFBMs != 60 {
		t.Fatalf("ttfb = avg %d p95 %d, want 40/60", row.AvgTTFBMs, row.P95TTFBMs)
	}
	if row.CaptureCaptured != 1 || row.CaptureFailed != 1 || row.CaptureSkipped != 1 {
		t.Fatalf("capture counts = %+v, want 1/1/1", row)
	}
	if row.LatestErrorStatus != 429 || row.LatestErrorEndpoint != "/v1/chat/completions" || row.LatestErrorModel != "gpt-4o" {
		t.Fatalf("latest error = %+v, want 429 chat completions gpt-4o", row)
	}
}

func TestActivityUsesBoundedRecentSample(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	records := make([]UsageRecord, 0, activitySampleLimit+5)
	for i := 0; i < activitySampleLimit+5; i++ {
		status := 200
		if i == 0 {
			status = 500
		}
		records = append(records, UsageRecord{
			CreatedAt: now.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			Endpoint:  "/v1/responses",
			Method:    "POST",
			Status:    status,
			LatencyMs: int64(i + 1),
			TTFBMs:    int64(i + 1),
		})
	}
	if err := d.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	row, err := d.Activity(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if row.SampleSize != activitySampleLimit {
		t.Fatalf("sample size = %d, want bounded sample %d", row.SampleSize, activitySampleLimit)
	}
	if row.FailedCount != 0 || row.LatestErrorStatus != 0 {
		t.Fatalf("activity should ignore errors outside recent sample, got %+v", row)
	}
}

func TestHealthMetrics(t *testing.T) {
	d := newTestDB(t)

	err := d.InsertHealthMetric(ts(-10*time.Minute), 5, 10, 2, 1, 0)
	if err != nil {
		t.Fatalf("InsertHealthMetric: %v", err)
	}

	h, err := d.LatestHealth()
	if err != nil {
		t.Fatalf("LatestHealth: %v", err)
	}
	if h.QueueDepth != 5 || h.DroppedEvents != 10 || h.ParseErrors != 2 || h.DBErrors != 1 {
		t.Errorf("health = %+v, want queue=5 dropped=10 parse=2 db=1", h)
	}

	rows, err := d.ErrorTimeline(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("ErrorTimeline: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 error timeline row, got %d", len(rows))
	}
	if len(rows) == 1 && (rows[0].DroppedEvents != 10 || rows[0].ParseErrors != 2 || rows[0].DBErrors != 1) {
		t.Errorf("error deltas = %+v, want dropped=10 parse=2 db=1", rows[0])
	}
	if err := d.InsertHealthMetric(ts(-5*time.Minute), 5, 12, 2, 5, 0); err != nil {
		t.Fatalf("InsertHealthMetric second: %v", err)
	}
	rows, err = d.ErrorTimeline(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("ErrorTimeline second: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 error timeline rows, got %d", len(rows))
	}
	if rows[1].DroppedEvents != 2 || rows[1].ParseErrors != 0 || rows[1].DBErrors != 4 {
		t.Errorf("second error deltas = %+v, want dropped=2 parse=0 db=4", rows[1])
	}
}

func TestErrorTimelineHandlesCounterReset(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.InsertHealthMetric(now.Add(-2*time.Hour).Format(time.RFC3339), 0, 10, 20, 30, 0); err != nil {
		t.Fatalf("InsertHealthMetric previous: %v", err)
	}
	if err := d.InsertHealthMetric(now.Add(-30*time.Minute).Format(time.RFC3339), 0, 1, 2, 3, 0); err != nil {
		t.Fatalf("InsertHealthMetric reset: %v", err)
	}

	rows, err := d.ErrorTimeline(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ErrorTimeline: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one in-range row, got %d", len(rows))
	}
	if rows[0].DroppedEvents != 1 || rows[0].ParseErrors != 2 || rows[0].DBErrors != 3 {
		t.Fatalf("reset delta = %+v, want dropped=1 parse=2 db=3", rows[0])
	}
}

func TestDBFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.sqlite"
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	d.Close()

	// Verify the main DB file exists, is non-empty, and has restricted permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("db file is empty")
	}
	// On Unix, 0600 means no group/other bits. On Windows the permission model
	// is different, so skip the hard assertion there.
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got&0077 != 0 {
			t.Errorf("db file permissions: %04o, want 0600 (no group/other access)", got)
		}
	}

	// Check WAL/SHM sidecar files if they exist.  On some platforms (e.g. Windows)
	// SQLite creates these lazily and they may not exist after Close().
	for _, suffix := range []string{"-wal", "-shm"} {
		sideInfo, err := os.Stat(path + suffix)
		if err != nil {
			continue // not an error if sidecar files don't exist
		}
		if runtime.GOOS != "windows" {
			if got := sideInfo.Mode().Perm(); got&0077 != 0 {
				t.Errorf("%s file permissions: %04o, want 0600", suffix, got)
			}
		}
	}
}

func TestOverview(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	if err := d.InsertBatch([]UsageRecord{
		{CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/chat/completions", Method: "POST", Status: 200, LatencyMs: 100, TTFBMs: 20, InputTokens: 500, OutputTokens: 200, TotalTokens: 700, CaptureOutcome: "captured"},
		{CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/chat/completions", Method: "POST", Status: 500, LatencyMs: 300, TTFBMs: 60, InputTokens: 100, OutputTokens: 50, TotalTokens: 150, CaptureOutcome: "failed"},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	row := d.Overview(now.Add(-time.Hour))
	if row == nil {
		t.Fatal("Overview returned nil")
	}
	if row.Selected.Error != "" {
		t.Fatalf("Overview.Selected error: %s", row.Selected.Error)
	}
	data, ok := row.Selected.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Overview.Selected.Data is not a map, got %T", row.Selected.Data)
	}
	totalReqs, _ := data["total_requests"].(int64)
	if totalReqs != 2 {
		t.Errorf("total_requests = %d, want 2", totalReqs)
	}
	failedReqs, _ := data["failed_requests"].(int64)
	if failedReqs != 1 {
		t.Errorf("failed_requests = %d, want 1", failedReqs)
	}
}

func TestInsertBatch_AllFieldsPopulated(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	fullRecord := UsageRecord{
		CreatedAt:             now.Add(-5 * time.Minute).Format(time.RFC3339),
		RequestID:             "req-test-all-fields",
		Endpoint:              "/v1/chat/completions",
		Method:                "POST",
		Status:                200,
		LatencyMs:             150,
		TTFBMs:                30,
		Stream:                true,
		ClientIPHash:          "ip-hash-123",
		APIKeyHash:            "key-hash-456",
		ModelRequested:        "gpt-4o",
		ModelReturned:         "gpt-4o-2024-08-06",
		InputTokens:           1000,
		OutputTokens:          500,
		ReasoningTokens:       50,
		CachedTokens:          200,
		CacheCreationTokens:   30,
		TotalTokens:           1550,
		RequestBytes:          2048,
		ResponseBytes:         8192,
		Error:                 "",
		EndpointProfile:       "chat_completions",
		CaptureMode:           "usage_metered",
		MeteringKind:          "llm_tokens",
		UsageRawJSON:          `{"prompt_tokens":1000}`,
		UsageRawTruncated:     false,
		BillableInput:         0.03,
		BillableOutput:        0.06,
		BillableTotal:         0.09,
		BillableUnit:          "USD",
		CaptureOutcome:        "captured",
		CaptureReason:         "",
		ErrorClass:            "",
		ErrorType:             "",
		ErrorCode:             "",
		ErrorParam:            "",
		ErrorMessage:          "",
		ErrorMessageTruncated: false,
	}

	if err := d.InsertBatch([]UsageRecord{fullRecord}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	rows, err := d.Requests(10, 0, 0, "", "", "", time.Time{})
	if err != nil {
		t.Fatalf("Requests: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.RequestID != "req-test-all-fields" {
		t.Errorf("request_id = %q, want req-test-all-fields", r.RequestID)
	}
	if r.ReasoningTokens != 50 {
		t.Errorf("reasoning_tokens = %d, want 50", r.ReasoningTokens)
	}
	if r.CacheCreationTokens != 30 {
		t.Errorf("cache_creation_tokens = %d, want 30", r.CacheCreationTokens)
	}
	if r.EndpointProfile != "chat_completions" {
		t.Errorf("endpoint_profile = %q, want chat_completions", r.EndpointProfile)
	}
	if r.CaptureOutcome != "captured" {
		t.Errorf("capture_outcome = %q, want captured", r.CaptureOutcome)
	}
}
