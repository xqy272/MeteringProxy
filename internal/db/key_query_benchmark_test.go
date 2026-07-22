package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkMillionRowKeyReports is an opt-in large-fixture benchmark.
//
//	go test ./internal/db -run '^$' -bench BenchmarkMillionRowKeyReports -benchtime=1x -count=1 -timeout=30m
//
// Normal `go test ./internal/db` does not create the million-row fixture.
func BenchmarkMillionRowKeyReports(b *testing.B) {
	ctx := context.Background()
	path := filepath.Join(b.TempDir(), "million_key_reports.sqlite")
	d, err := Open(path)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = d.Close() })

	const (
		rowCount   = 1_000_000
		keyCount   = 100
		modelCount = 100
	)
	now := time.Now().UTC().Truncate(time.Second)
	setupStart := time.Now()
	if err := seedMillionRowKeyFixture(d, now, rowCount, keyCount, modelCount); err != nil {
		b.Fatalf("seed fixture: %v", err)
	}
	setupElapsed := time.Since(setupStart)

	var gotRows, gotKeys, gotModels int64
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM request_usage`).Scan(&gotRows); err != nil {
		b.Fatalf("count rows: %v", err)
	}
	if err := d.sql.QueryRow(`SELECT COUNT(DISTINCT api_key_hash) FROM request_usage WHERE NULLIF(TRIM(api_key_hash), '') IS NOT NULL`).Scan(&gotKeys); err != nil {
		b.Fatalf("count keys: %v", err)
	}
	if err := d.sql.QueryRow(`SELECT COUNT(DISTINCT model_returned) FROM request_usage`).Scan(&gotModels); err != nil {
		b.Fatalf("count models: %v", err)
	}
	if gotRows != rowCount {
		b.Fatalf("fixture rows = %d, want %d", gotRows, rowCount)
	}
	if gotKeys != keyCount {
		b.Fatalf("fixture keys = %d, want %d", gotKeys, keyCount)
	}
	if gotModels != modelCount {
		b.Fatalf("fixture models = %d, want %d", gotModels, modelCount)
	}

	if _, err := d.CheckpointWAL("TRUNCATE"); err != nil {
		b.Fatalf("checkpoint: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatalf("stat db: %v", err)
	}
	b.Logf("fixture setup=%s rows=%d keys=%d models=%d db_bytes=%d path=%s",
		setupElapsed, gotRows, gotKeys, gotModels, info.Size(), path)

	// Stable exact Key used by all Key-scoped sub-benchmarks.
	key := fmt.Sprintf("%064d", 0)
	since24h := now.Add(-24 * time.Hour)
	since7d := now.Add(-7 * 24 * time.Hour)
	since30d := now.Add(-30 * 24 * time.Hour)
	scope24 := ReportScope{Since: since24h, KeyHash: key}
	scope7 := ReportScope{Since: since7d, KeyHash: key}
	scope30 := ReportScope{Since: since30d, KeyHash: key}

	// Capture one representative EXPLAIN outside timed loops.
	plan := explainQueryPlanB(b, d,
		`SELECT COUNT(*) FROM request_usage WHERE created_at_unix >= ? AND api_key_hash = ?`,
		since24h.Unix(), key,
	)
	b.Logf("exact-key count plan:\n%s", plan)

	b.Run("KeysList_24h", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := d.KeysReportSnapshot(ctx, since24h); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("KeysList_7d", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := d.KeysReportSnapshot(ctx, since7d); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("KeysList_30d", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := d.KeysReportSnapshot(ctx, since30d); err != nil {
				b.Fatal(err)
			}
		}
	})

	for _, tc := range []struct {
		name  string
		scope ReportScope
	}{
		{"Key24h", scope24},
		{"Key7d", scope7},
		{"Key30d", scope30},
	} {
		scope := tc.scope
		b.Run(tc.name+"/Models", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := d.ModelsReportSnapshot(ctx, scope); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/Timeseries", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := d.TimeseriesReportSnapshot(ctx, scope, 60); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/Activity", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := d.ActivityReport(ctx, scope); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/Issues", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := d.IssuesReport(ctx, IssueFilter{Scope: scope, Limit: 20, IncludeGlobal: false}); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/Requests", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := d.RequestsReport(ctx, RequestFilter{Scope: scope, Limit: 100}); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/CostBuckets", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, _, err := d.CostBucketsContext(ctx, CostBucketFilter{Since: scope.Since, KeyHash: scope.KeyHash}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func seedMillionRowKeyFixture(d *DB, now time.Time, rowCount, keyCount, modelCount int) error {
	// Fixture-only pragmas: keep production Open path unchanged.
	if _, err := d.sql.Exec(`PRAGMA synchronous = OFF`); err != nil {
		return err
	}
	if _, err := d.sql.Exec(`PRAGMA temp_store = MEMORY`); err != nil {
		return err
	}
	if _, err := d.sql.Exec(`PRAGMA cache_size = -200000`); err != nil {
		return err
	}

	// Drop secondary indexes during bulk load, then recreate via createIndexes.
	// This keeps setup efficient while preserving the production index set for timed queries.
	rows, err := d.sql.Query(`SELECT name FROM sqlite_master WHERE type='index' AND sql IS NOT NULL AND tbl_name='request_usage'`)
	if err != nil {
		return err
	}
	var indexNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		indexNames = append(indexNames, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range indexNames {
		if _, err := d.sql.Exec(`DROP INDEX IF EXISTS ` + quoteIdent(name)); err != nil {
			return err
		}
	}

	// Build 1,000,000 rows via two 1000-way recursive dimensions (no Go row loop).
	// Time span covers at least 30d so 24h/7d/30d scopes are non-empty.
	endUnix := now.Unix()
	spanSec := int64(30 * 24 * 60 * 60)
	_, err = d.sql.Exec(fmt.Sprintf(`
		WITH RECURSIVE
			ones(x) AS (
				SELECT 0
				UNION ALL
				SELECT x + 1 FROM ones WHERE x < 999
			),
			thousands(y) AS (
				SELECT 0
				UNION ALL
				SELECT y + 1 FROM thousands WHERE y < 999
			)
		INSERT INTO request_usage (
			created_at, created_at_unix, request_id, endpoint, method, status, latency_ms, ttfb_ms, stream,
			api_key_hash, model_requested, model_returned,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_creation_tokens, total_tokens,
			request_bytes, response_bytes,
			endpoint_profile, capture_mode, metering_kind, capture_outcome, capture_reason,
			error_class, model_returned_source, usage_source, terminal_event, terminal_reason
		)
		SELECT
			strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', %d - ((y * 1000 + x) %% %d), 'unixepoch'),
			%d - ((y * 1000 + x) %% %d),
			'bench_' || (y * 1000 + x),
			CASE (y * 1000 + x) %% 3
				WHEN 0 THEN '/v1/chat/completions'
				WHEN 1 THEN '/v1/responses'
				ELSE '/v1/messages'
			END,
			'POST',
			CASE WHEN (y * 1000 + x) %% 17 = 0 THEN 500 ELSE 200 END,
			40 + ((y * 1000 + x) %% 200),
			5 + ((y * 1000 + x) %% 40),
			(y * 1000 + x) %% 2,
			printf('%%064d', (y * 1000 + x) %% %d),
			printf('alias-%%03d', (y * 1000 + x) %% %d),
			printf('model-%%03d', (y * 1000 + x) %% %d),
			100 + ((y * 1000 + x) %% 500),
			50 + ((y * 1000 + x) %% 200),
			CASE WHEN (y * 1000 + x) %% 11 = 0 THEN 20 ELSE 0 END,
			CASE WHEN (y * 1000 + x) %% 9 = 0 THEN 30 ELSE 0 END,
			CASE WHEN (y * 1000 + x) %% 13 = 0 THEN 10 ELSE 0 END,
			150 + ((y * 1000 + x) %% 700),
			1000 + ((y * 1000 + x) %% 5000),
			2000 + ((y * 1000 + x) %% 8000),
			'openai_chat_completions',
			CASE
				WHEN (y * 1000 + x) %% 23 = 0 THEN 'request_only'
				ELSE 'usage_metered'
			END,
			'tokens',
			CASE
				WHEN (y * 1000 + x) %% 17 = 0 THEN 'failed'
				WHEN (y * 1000 + x) %% 23 = 0 THEN 'skipped'
				ELSE 'captured'
			END,
			CASE
				WHEN (y * 1000 + x) %% 17 = 0 THEN 'upstream_error'
				WHEN (y * 1000 + x) %% 23 = 0 THEN 'request_only'
				ELSE ''
			END,
			CASE WHEN (y * 1000 + x) %% 17 = 0 THEN 'proxy_upstream_error' ELSE '' END,
			'response_body',
			CASE
				WHEN (y * 1000 + x) %% 23 = 0 THEN 'none'
				WHEN (y * 1000 + x) %% 19 = 0 THEN 'cliproxy_side_channel'
				ELSE 'http_response'
			END,
			'done',
			''
		FROM ones, thousands
	`, endUnix, spanSec, endUnix, spanSec, keyCount, modelCount, modelCount))
	if err != nil {
		return fmt.Errorf("bulk insert request_usage: %w", err)
	}

	// Small image_usage mix for cost-bucket realism (not timed separately).
	if _, err := d.sql.Exec(fmt.Sprintf(`
		INSERT INTO image_usage (
			request_usage_id, request_id, created_at, created_at_unix,
			operation, provider, model_requested, model_returned, size, quality, output_format,
			stream, image_count, partial_image_count, input_image_count, has_mask,
			usage_source, capture_outcome, capture_reason, metadata_json
		)
		SELECT
			id, request_id, created_at, created_at_unix,
			'generate', 'openai', model_requested, model_returned,
			CASE id %% 3 WHEN 0 THEN '1024x1024' WHEN 1 THEN '1024x1536' ELSE '2048x2048' END,
			'standard', 'png',
			0, 1, 0, 1, 0,
			'http_response', 'captured', '', '{}'
		FROM request_usage
		WHERE id %% 2000 = 0
		LIMIT 500
	`)); err != nil {
		return fmt.Errorf("seed image_usage: %w", err)
	}

	if err := createIndexes(d.sql); err != nil {
		return fmt.Errorf("recreate indexes: %w", err)
	}
	// Restore durable settings after fixture construction.
	if _, err := d.sql.Exec(`PRAGMA synchronous = NORMAL`); err != nil {
		return err
	}
	_ = rowCount // documented expected size; verified by caller
	return nil
}

func explainQueryPlanB(b *testing.B, d *DB, query string, args ...any) string {
	b.Helper()
	rows, err := d.read.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		b.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			b.Fatalf("scan explain: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		b.Fatalf("explain rows: %v", err)
	}
	out := ""
	for i, d := range details {
		if i > 0 {
			out += "\n"
		}
		out += d
	}
	return out
}
