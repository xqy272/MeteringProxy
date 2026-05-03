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

	if err := d.InsertHealthMetric(ts(0), 1, 2, 3, 4); err != nil {
		t.Fatalf("InsertHealthMetric after legacy migration: %v", err)
	}

	rows, err := d.Requests(10, 0, 0, "", "", time.Time{})
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

func TestModelsAndKeysTreatEmptyAsUnknown(t *testing.T) {
	d := newTestDB(t)
	insertRecord(t, d, ts(-1*time.Minute), "/v1/chat/completions", 200, "", 1, 2, 3)

	models, err := d.Models(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0].Model != "unknown" {
		t.Fatalf("models = %+v, want one unknown row", models)
	}

	keys, err := d.Keys(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 || keys[0].KeyHash != "unknown" {
		t.Fatalf("keys = %+v, want one unknown row", keys)
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

	all, err := d.Requests(100, 0, 0, "", "", time.Time{})
	if err != nil {
		t.Fatalf("Requests(all): %v", err)
	}
	if len(all) != 7 {
		t.Errorf("all: got %d rows, want 7", len(all))
	}

	success, err := d.Requests(100, 200, 300, "", "", time.Time{})
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

	clientErrors, err := d.Requests(100, 400, 500, "", "", time.Time{})
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

	serverErrors, err := d.Requests(100, 500, 0, "", "", time.Time{})
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
	rows, err := d.Requests(100, 0, 0, "", "", since24h)
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
	rowsAll, err := d.Requests(100, 0, 0, "", "", since7d)
	if err != nil {
		t.Fatalf("Requests with 7d range: %v", err)
	}
	if len(rowsAll) != 2 {
		t.Errorf("7d range: got %d rows, want 2 (both records)", len(rowsAll))
	}

	rowsZeroSince, err := d.Requests(100, 0, 0, "", "", time.Time{})
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
	if rows[1].Count != 1 {
		t.Errorf("bucket 2 count = %d, want 1", rows[1].Count)
	}
}

func TestHealthMetrics(t *testing.T) {
	d := newTestDB(t)

	err := d.InsertHealthMetric(ts(-10*time.Minute), 5, 10, 2, 1)
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
