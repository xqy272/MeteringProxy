package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const keyCreatedAtUnixIndex = "idx_request_usage_key_created_at_unix"

func TestKeyCreatedAtUnixIndexOnFreshDB(t *testing.T) {
	d := newTestDB(t)
	cols := indexColumns(t, d, keyCreatedAtUnixIndex)
	if len(cols) != 2 || cols[0] != "api_key_hash" || cols[1] != "created_at_unix" {
		t.Fatalf("index columns = %v, want [api_key_hash created_at_unix]", cols)
	}
}

func TestKeyCreatedAtUnixIndexUpgradeFromPreIndexDB(t *testing.T) {
	path := t.TempDir() + "/pre_index.sqlite"

	raw, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE request_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TEXT NOT NULL,
			created_at_unix INTEGER DEFAULT 0,
			request_id TEXT,
			endpoint TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			stream INTEGER NOT NULL DEFAULT 0,
			api_key_hash TEXT,
			model_returned TEXT,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0
		);
		CREATE INDEX idx_request_usage_created_at_unix ON request_usage(created_at_unix);
		CREATE INDEX idx_request_usage_key ON request_usage(api_key_hash);
		INSERT INTO request_usage (
			created_at, created_at_unix, request_id, endpoint, method, status, latency_ms, stream,
			api_key_hash, model_returned, input_tokens, output_tokens, total_tokens
		) VALUES
			('2026-07-01T00:00:00Z', 1719792000, 'req-pre-1', '/v1/chat/completions', 'POST', 200, 10, 0, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'gpt-4o', 1, 2, 3),
			('2026-07-01T00:01:00Z', 1719792060, 'req-pre-2', '/v1/chat/completions', 'POST', 500, 20, 0, '', 'gpt-4o', 4, 5, 9);
	`); err != nil {
		raw.Close()
		t.Fatalf("seed pre-index db: %v", err)
	}
	raw.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open pre-index db: %v", err)
	}
	defer d.Close()

	var count int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM request_usage`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("row count = %d, want 2 preserved", count)
	}
	cols := indexColumns(t, d, keyCreatedAtUnixIndex)
	if len(cols) != 2 || cols[0] != "api_key_hash" || cols[1] != "created_at_unix" {
		t.Fatalf("upgraded index columns = %v, want [api_key_hash created_at_unix]", cols)
	}

	rows, err := d.RequestsReport(context.Background(), RequestFilter{
		Scope: ReportScope{Since: time.Unix(1719792000, 0).UTC(), KeyHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("RequestsReport after upgrade: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "req-pre-1" {
		t.Fatalf("unexpected rows after upgrade: %+v", rows)
	}
}

func TestKeyCreatedAtUnixIndexMigrationIdempotent(t *testing.T) {
	path := t.TempDir() + "/idempotent.sqlite"
	d1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := d1.InsertBatch([]UsageRecord{{
		CreatedAt:     ts(-time.Minute),
		Endpoint:      "/v1/chat/completions",
		Method:        "POST",
		Status:        200,
		LatencyMs:     5,
		APIKeyHash:    strings.Repeat("ab", 32),
		ModelReturned: "gpt-4o",
		InputTokens:   1,
		OutputTokens:  1,
		TotalTokens:   2,
	}}); err != nil {
		d1.Close()
		t.Fatalf("InsertBatch: %v", err)
	}
	d1.Close()

	d2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer d2.Close()
	cols := indexColumns(t, d2, keyCreatedAtUnixIndex)
	if len(cols) != 2 {
		t.Fatalf("index missing after re-open: %v", cols)
	}
	var count int
	if err := d2.sql.QueryRow(`SELECT COUNT(*) FROM request_usage`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after re-open = %d, want 1", count)
	}

	d2.Close()
	d3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open: %v", err)
	}
	defer d3.Close()
	if got := indexColumns(t, d3, keyCreatedAtUnixIndex); len(got) != 2 {
		t.Fatalf("index missing after third Open: %v", got)
	}
}

func TestKeyCreatedAtUnixIndexToleratesExtraUnknownIndex(t *testing.T) {
	path := t.TempDir() + "/extra_index.sqlite"
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := d.sql.Exec(`CREATE INDEX IF NOT EXISTS idx_request_usage_extra_unknown ON request_usage(request_id)`); err != nil {
		d.Close()
		t.Fatalf("create extra index: %v", err)
	}
	if err := d.InsertBatch([]UsageRecord{{
		CreatedAt:     ts(-time.Minute),
		Endpoint:      "/v1/chat/completions",
		Method:        "POST",
		Status:        200,
		LatencyMs:     5,
		RequestID:     "req-extra",
		APIKeyHash:    strings.Repeat("cd", 32),
		ModelReturned: "gpt-4o",
		InputTokens:   1,
		OutputTokens:  1,
		TotalTokens:   2,
	}}); err != nil {
		d.Close()
		t.Fatalf("InsertBatch: %v", err)
	}
	d.Close()

	d2, err := Open(path)
	if err != nil {
		t.Fatalf("re-open with extra index: %v", err)
	}
	defer d2.Close()

	if got := indexColumns(t, d2, keyCreatedAtUnixIndex); len(got) != 2 {
		t.Fatalf("required index missing: %v", got)
	}
	if got := indexColumns(t, d2, "idx_request_usage_extra_unknown"); len(got) != 1 || got[0] != "request_id" {
		t.Fatalf("extra unknown index not preserved: %v", got)
	}
	var count int
	if err := d2.sql.QueryRow(`SELECT COUNT(*) FROM request_usage`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1", count)
	}
}

func TestExactKeyQueryPlansUseCompositeIndex(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	keyA := strings.Repeat("11", 32)
	keyB := strings.Repeat("22", 32)
	records := make([]UsageRecord, 0, 40)
	for i := 0; i < 20; i++ {
		records = append(records, UsageRecord{
			CreatedAt:           now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339),
			Endpoint:            "/v1/chat/completions",
			Method:              "POST",
			Status:              200,
			LatencyMs:           int64(40 + i),
			TTFBMs:              int64(5 + i),
			APIKeyHash:          keyA,
			ModelReturned:       fmt.Sprintf("model-%02d", i%5),
			InputTokens:         10,
			OutputTokens:        5,
			TotalTokens:         15,
			CaptureOutcome:      "captured",
			UsageSource:         "http_response",
			ModelReturnedSource: "response_body",
		})
		status := 200
		errClass := ""
		if i%4 == 0 {
			status = 500
			errClass = "proxy_upstream_error"
		}
		records = append(records, UsageRecord{
			CreatedAt:           now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339),
			Endpoint:            "/v1/responses",
			Method:              "POST",
			Status:              status,
			LatencyMs:           int64(50 + i),
			TTFBMs:              int64(6 + i),
			APIKeyHash:          keyB,
			ModelReturned:       fmt.Sprintf("alias-%02d", i%3),
			InputTokens:         8,
			OutputTokens:        4,
			TotalTokens:         12,
			CaptureOutcome:      "captured",
			UsageSource:         "http_response",
			ModelReturnedSource: "response_body",
			ErrorClass:          errClass,
		})
	}
	records = append(records, UsageRecord{
		CreatedAt:      now.Add(-2 * time.Minute).Format(time.RFC3339),
		Endpoint:       "/v1/chat/completions",
		Method:         "POST",
		Status:         200,
		LatencyMs:      11,
		APIKeyHash:     "",
		ModelReturned:  "unknown-model",
		InputTokens:    1,
		OutputTokens:   1,
		TotalTokens:    2,
		CaptureOutcome: "captured",
		UsageSource:    "http_response",
	}, UsageRecord{
		CreatedAt:      now.Add(-3 * time.Minute).Format(time.RFC3339),
		Endpoint:       "/v1/chat/completions",
		Method:         "POST",
		Status:         200,
		LatencyMs:      12,
		APIKeyHash:     "   ",
		ModelReturned:  "unknown-model",
		InputTokens:    1,
		OutputTokens:   1,
		TotalTokens:    2,
		CaptureOutcome: "captured",
		UsageSource:    "http_response",
	})
	if err := d.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	since := now.Add(-30 * time.Minute).Unix()
	cases := []struct {
		name      string
		query     string
		args      []any
		wantIndex string
	}{
		{
			name:      "requests_recent_limit",
			query:     `SELECT id FROM request_usage WHERE created_at_unix >= ? AND api_key_hash = ? ORDER BY id DESC LIMIT ?`,
			args:      []any{since, keyA, 100},
			wantIndex: keyCreatedAtUnixIndex,
		},
		{
			name:      "activity_sample_scope",
			query:     `SELECT id, latency_ms, ttfb_ms FROM request_usage WHERE created_at_unix >= ? AND api_key_hash = ? ORDER BY id DESC LIMIT ?`,
			args:      []any{since, keyA, activitySampleLimit},
			wantIndex: keyCreatedAtUnixIndex,
		},
		{
			name:      "percentile_sample_scope",
			query:     `SELECT latency_ms FROM request_usage WHERE created_at_unix >= ? AND api_key_hash = ? ORDER BY id DESC LIMIT ?`,
			args:      []any{since, keyA, activitySampleLimit},
			wantIndex: keyCreatedAtUnixIndex,
		},
		{
			name: "models_core_scope",
			query: `SELECT ` + effectiveModelExpr + `, COUNT(*)
				FROM request_usage
				WHERE created_at_unix >= ? AND api_key_hash = ?
				GROUP BY 1`,
			args:      []any{since, keyA},
			wantIndex: keyCreatedAtUnixIndex,
		},
		{
			name: "timeseries_core_scope",
			query: `SELECT strftime('%Y-%m-%dT%H:%M:%SZ', (created_at_unix / 600) * 600, 'unixepoch'), COUNT(*)
				FROM request_usage
				WHERE created_at_unix >= ? AND api_key_hash = ?
				GROUP BY 1`,
			args:      []any{since, keyA},
			wantIndex: keyCreatedAtUnixIndex,
		},
		{
			name: "issues_core_scope",
			query: `SELECT id FROM request_usage
				WHERE created_at_unix >= ? AND api_key_hash = ?
				  AND (status >= 400 OR (capture_outcome = 'failed' AND capture_reason != '') OR (capture_outcome = 'skipped' AND capture_reason != ''))`,
			args:      []any{since, keyB},
			wantIndex: keyCreatedAtUnixIndex,
		},
		{
			name:      "cost_buckets_core_scope",
			query:     `SELECT COUNT(*) FROM request_usage ru WHERE ru.created_at_unix >= ? AND ru.api_key_hash = ?`,
			args:      []any{since, keyA},
			wantIndex: keyCreatedAtUnixIndex,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainQueryPlan(t, d, tc.query, tc.args...)
			if !strings.Contains(strings.ToLower(plan), strings.ToLower(tc.wantIndex)) {
				t.Fatalf("query plan did not use %s\nplan:\n%s", tc.wantIndex, plan)
			}
		})
	}

	t.Run("unknown_key_avoids_composite_equality_seek", func(t *testing.T) {
		// unknown maps to NULLIF(TRIM(api_key_hash), '') IS NULL. That expression cannot
		// perform equality seeks on idx_request_usage_key_created_at_unix. SQLite may use
		// idx_request_usage_created_at_unix for the time predicate, or SCAN on small tables.
		plan := explainQueryPlan(t, d,
			`SELECT id FROM request_usage WHERE created_at_unix >= ? AND NULLIF(TRIM(api_key_hash), '') IS NULL ORDER BY id DESC LIMIT ?`,
			since, 100,
		)
		lower := strings.ToLower(plan)
		if strings.Contains(lower, strings.ToLower(keyCreatedAtUnixIndex)) &&
			strings.Contains(lower, "api_key_hash=?") {
			t.Fatalf("unexpected composite equality seek for unknown key\nplan:\n%s", plan)
		}
		// Accept time-index range access or a plain scan; both preserve correct unknown semantics.
		if !(strings.Contains(lower, "idx_request_usage_created_at_unix") ||
			strings.Contains(lower, "scan request_usage") ||
			strings.Contains(lower, "created_at_unix")) {
			t.Fatalf("unexpected unknown-key plan\nplan:\n%s", plan)
		}
	})
}

func TestExactKeyReportsHonorCanceledContextPromptly(t *testing.T) {
	d := newTestDB(t)
	if err := d.InsertBatch([]UsageRecord{{
		CreatedAt:     ts(-time.Minute),
		Endpoint:      "/v1/chat/completions",
		Method:        "POST",
		Status:        200,
		LatencyMs:     5,
		APIKeyHash:    strings.Repeat("ef", 32),
		ModelReturned: "gpt-4o",
		InputTokens:   1,
		OutputTokens:  1,
		TotalTokens:   2,
	}}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scope := ReportScope{Since: time.Now().Add(-time.Hour), KeyHash: strings.Repeat("ef", 32)}

	checks := []struct {
		name string
		fn   func() error
	}{
		{"ModelsReportSnapshot", func() error {
			_, err := d.ModelsReportSnapshot(ctx, scope)
			return err
		}},
		{"TimeseriesReportSnapshot", func() error {
			_, err := d.TimeseriesReportSnapshot(ctx, scope, 60)
			return err
		}},
		{"ActivityReport", func() error {
			_, err := d.ActivityReport(ctx, scope)
			return err
		}},
		{"IssuesReport", func() error {
			_, err := d.IssuesReport(ctx, IssueFilter{Scope: scope, Limit: 20})
			return err
		}},
		{"RequestsReport", func() error {
			_, err := d.RequestsReport(ctx, RequestFilter{Scope: scope, Limit: 20})
			return err
		}},
		{"CostBucketsContext", func() error {
			_, _, err := d.CostBucketsContext(ctx, CostBucketFilter{Since: scope.Since, KeyHash: scope.KeyHash})
			return err
		}},
		{"KeysReportSnapshot", func() error {
			_, err := d.KeysReportSnapshot(ctx, scope.Since)
			return err
		}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			err := tc.fn()
			elapsed := time.Since(start)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if elapsed > 500*time.Millisecond {
				t.Fatalf("canceled context took %s, want prompt return", elapsed)
			}
		})
	}
}

func TestCanceledSQLiteQueryReleasesReadConnection(t *testing.T) {
	d := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var sum int64
		err := d.read.QueryRowContext(ctx, `
			WITH RECURSIVE sequence(x) AS (
				SELECT 0
				UNION ALL
				SELECT x + 1 FROM sequence WHERE x < 1000000000
			)
			SELECT SUM(x) FROM sequence
		`).Scan(&sum)
		done <- err
	}()

	// The query is deliberately too large to finish here. Observing it still
	// running before cancellation distinguishes this from a pre-canceled call.
	select {
	case err := <-done:
		t.Fatalf("long query finished before cancellation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("long query error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight SQLite query did not stop after cancellation")
	}

	probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
	defer probeCancel()
	var schemaVersion int64
	if err := d.read.QueryRowContext(probeCtx, "PRAGMA main.schema_version").Scan(&schemaVersion); err != nil {
		t.Fatalf("read connection was not released after cancellation: %v", err)
	}
}

func TestExactKeyReportReadDoesNotBlockWriterUnderWAL(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	key := strings.Repeat("aa", 32)
	seed := make([]UsageRecord, 0, 200)
	for i := 0; i < 200; i++ {
		seed = append(seed, UsageRecord{
			CreatedAt:     now.Add(-time.Duration(i) * time.Second).Format(time.RFC3339),
			Endpoint:      "/v1/chat/completions",
			Method:        "POST",
			Status:        200,
			LatencyMs:     10,
			APIKeyHash:    key,
			ModelReturned: "gpt-4o",
			InputTokens:   1,
			OutputTokens:  1,
			TotalTokens:   2,
		})
	}
	if err := d.InsertBatch(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	readStarted := make(chan struct{})
	readHold := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		tx, err := d.read.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			readErr <- err
			return
		}
		defer func() { _ = tx.Rollback() }()
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM request_usage WHERE api_key_hash = ? AND created_at_unix >= ?`, key, now.Add(-time.Hour).Unix()).Scan(&n); err != nil {
			readErr <- err
			return
		}
		close(readStarted)
		<-readHold
		if err := tx.Commit(); err != nil {
			readErr <- err
			return
		}
		readErr <- nil
	}()

	select {
	case <-readStarted:
	case err := <-readErr:
		t.Fatalf("reader failed before start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reader to start")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- d.InsertBatch([]UsageRecord{{
			CreatedAt:     now.Format(time.RFC3339),
			Endpoint:      "/v1/chat/completions",
			Method:        "POST",
			Status:        200,
			LatencyMs:     7,
			APIKeyHash:    key,
			ModelReturned: "gpt-4o",
			InputTokens:   2,
			OutputTokens:  2,
			TotalTokens:   4,
			RequestID:     "writer-while-read",
		}})
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			close(readHold)
			t.Fatalf("writer blocked or failed while read tx active: %v", err)
		}
	case <-time.After(3 * time.Second):
		close(readHold)
		t.Fatal("writer did not complete promptly; likely waiting on reader lock")
	}

	close(readHold)
	select {
	case err := <-readErr:
		if err != nil {
			t.Fatalf("reader failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not finish")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := d.ModelsReportSnapshot(context.Background(), ReportScope{Since: now.Add(-time.Hour), KeyHash: key})
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		errCh <- d.InsertBatch([]UsageRecord{{
			CreatedAt:     now.Add(time.Second).Format(time.RFC3339),
			Endpoint:      "/v1/chat/completions",
			Method:        "POST",
			Status:        200,
			LatencyMs:     8,
			APIKeyHash:    key,
			ModelReturned: "gpt-4o",
			InputTokens:   3,
			OutputTokens:  3,
			TotalTokens:   6,
			RequestID:     "concurrent-write",
		}})
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent read/write failed: %v", err)
		}
	}
}

func indexColumns(t *testing.T, d *DB, name string) []string {
	t.Helper()
	rows, err := d.sql.Query(`PRAGMA index_info(` + quoteIdent(name) + `)`)
	if err != nil {
		t.Fatalf("PRAGMA index_info(%s): %v", name, err)
	}
	defer rows.Close()
	type col struct {
		seq  int
		name string
	}
	var cols []col
	for rows.Next() {
		var seqno, cid int
		var cname string
		if err := rows.Scan(&seqno, &cid, &cname); err != nil {
			t.Fatalf("scan index_info: %v", err)
		}
		cols = append(cols, col{seq: seqno, name: cname})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("index_info rows: %v", err)
	}
	if len(cols) == 0 {
		var exists int
		if err := d.sql.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&exists); err != nil {
			t.Fatalf("lookup index %s: %v", name, err)
		}
		if exists == 0 {
			t.Fatalf("index %s does not exist", name)
		}
	}
	out := make([]string, len(cols))
	for _, c := range cols {
		if c.seq < 0 || c.seq >= len(out) {
			t.Fatalf("unexpected index seq %d for %s", c.seq, name)
		}
		out[c.seq] = c.name
	}
	return out
}

func explainQueryPlan(t *testing.T, d *DB, query string, args ...any) string {
	t.Helper()
	rows, err := d.read.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v\nquery: %s", err, query)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan explain: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows: %v", err)
	}
	if len(details) == 0 {
		t.Fatal("empty EXPLAIN QUERY PLAN")
	}
	return strings.Join(details, "\n")
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
