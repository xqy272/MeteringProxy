package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type UsageRecord struct {
	CreatedAt       string
	RequestID       string
	Endpoint        string
	Method          string
	Status          int
	LatencyMs       int64
	TTFBMs          int64
	Stream          bool
	ClientIPHash    string
	APIKeyHash      string
	ModelRequested  string
	ModelReturned   string
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	CachedTokens    int64
	TotalTokens     int64
	RequestBytes    int64
	ResponseBytes   int64
	Error           string
}

type SummaryRow struct {
	TotalRequests        int64   `json:"total_requests"`
	FailedRequests       int64   `json:"failed_requests"`
	TotalInputTokens     int64   `json:"total_input_tokens"`
	TotalOutputTokens    int64   `json:"total_output_tokens"`
	TotalReasoningTokens int64   `json:"total_reasoning_tokens"`
	TotalCachedTokens    int64   `json:"total_cached_tokens"`
	TotalTokens          int64   `json:"total_tokens"`
	TotalCost            float64 `json:"total_cost"`
}

type ModelRow struct {
	Model           string `json:"model"`
	RequestCount    int64  `json:"request_count"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
}

type KeyRow struct {
	KeyHash      string `json:"key_hash"`
	RequestCount int64  `json:"request_count"`
	FailedCount  int64  `json:"failed_count"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

type TimeseriesRow struct {
	Timestamp    string `json:"timestamp"`
	Count        int64  `json:"count"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

type RequestRow struct {
	ID              int64  `json:"id"`
	CreatedAt       string `json:"created_at"`
	RequestID       string `json:"request_id"`
	Endpoint        string `json:"endpoint"`
	Method          string `json:"method"`
	Status          int    `json:"status"`
	LatencyMs       int64  `json:"latency_ms"`
	TTFBMs          int64  `json:"ttfb_ms"`
	Stream          bool   `json:"stream"`
	ClientIPHash    string `json:"client_ip_hash"`
	APIKeyHash      string `json:"api_key_hash"`
	ModelRequested  string `json:"model_requested"`
	ModelReturned   string `json:"model_returned"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	RequestBytes    int64  `json:"request_bytes"`
	ResponseBytes   int64  `json:"response_bytes"`
	Error           string `json:"error"`
}

type HealthRow struct {
	Timestamp     string `json:"timestamp"`
	QueueDepth    int64  `json:"queue_depth"`
	DroppedEvents int64  `json:"dropped_events"`
	ParseErrors   int64  `json:"parse_errors"`
	DBErrors      int64  `json:"db_errors"`
}

type ErrorTimelineRow struct {
	Timestamp     string `json:"timestamp"`
	Count         int64  `json:"count"`
	ParseErrors   int64  `json:"parse_errors"`
	DBErrors      int64  `json:"db_errors"`
	DroppedEvents int64  `json:"dropped_events"`
}

type DB struct {
	sql *sql.DB
}

const schemaVersion = 3

func Open(path string) (*DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	sqlDB, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Restrict permissions on the main database file.
	if err := os.Chmod(path, 0600); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("chmod db: %w", err)
	}

	// Trigger WAL/SHM sidecar creation so we can lock them down too.
	if _, err := sqlDB.Exec("CREATE TABLE IF NOT EXISTS _init (x)"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("init sidecar trigger: %w", err)
	}
	if _, err := sqlDB.Exec("DROP TABLE IF EXISTS _init"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("init sidecar cleanup: %w", err)
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		sidePath := path + suffix
		info, err := os.Stat(sidePath)
		if err != nil {
			continue
		}
		if info.Mode().Perm()&0077 != 0 {
			if cerr := os.Chmod(sidePath, 0600); cerr != nil {
				sqlDB.Close()
				return nil, fmt.Errorf("chmod %s: %w", suffix, cerr)
			}
		}
	}

	return &DB{sql: sqlDB}, nil
}

func migrate(sqlDB *sql.DB) error {
	schema := `
	PRAGMA journal_mode = WAL;
	PRAGMA synchronous = NORMAL;

	CREATE TABLE IF NOT EXISTS request_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL,
		created_at_unix INTEGER DEFAULT 0,
		request_id TEXT,
		endpoint TEXT NOT NULL,
		method TEXT NOT NULL,
		status INTEGER NOT NULL,
		latency_ms INTEGER NOT NULL,
		stream INTEGER NOT NULL,
		client_ip_hash TEXT,
		api_key_hash TEXT,
		model_requested TEXT,
		model_returned TEXT,
		input_tokens INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		reasoning_tokens INTEGER DEFAULT 0,
		cached_tokens INTEGER DEFAULT 0,
		total_tokens INTEGER DEFAULT 0,
		request_bytes INTEGER DEFAULT 0,
		response_bytes INTEGER DEFAULT 0,
		error TEXT
	);

	CREATE TABLE IF NOT EXISTS health_metrics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp TEXT NOT NULL,
		timestamp_unix INTEGER DEFAULT 0,
		queue_depth INTEGER DEFAULT 0,
		dropped_events_total INTEGER DEFAULT 0,
		parse_error_total INTEGER DEFAULT 0,
		db_write_error_total INTEGER DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	);
	`
	if _, err := sqlDB.Exec(schema); err != nil {
		return err
	}
	if err := ensureColumns(sqlDB, "request_usage", requestUsageColumns()); err != nil {
		return err
	}
	if err := ensureColumns(sqlDB, "health_metrics", healthMetricColumns()); err != nil {
		return err
	}
	if err := createIndexes(sqlDB); err != nil {
		return err
	}
	if err := runBackfills(sqlDB); err != nil {
		return err
	}
	if _, err := sqlDB.Exec(`
		INSERT OR IGNORE INTO schema_migrations (version, applied_at)
		VALUES (?, ?)
	`, schemaVersion, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	_, err := sqlDB.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion))
	return err
}

func createIndexes(sqlDB *sql.DB) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_request_usage_created_at ON request_usage(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_created_at_unix ON request_usage(created_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_model ON request_usage(model_returned)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_key ON request_usage(api_key_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_status ON request_usage(status)`,
		`CREATE INDEX IF NOT EXISTS idx_health_metrics_timestamp ON health_metrics(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_health_metrics_timestamp_unix ON health_metrics(timestamp_unix)`,
	}
	for _, stmt := range indexes {
		if _, err := sqlDB.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

type columnSpec struct {
	name string
	ddl  string
}

func requestUsageColumns() []columnSpec {
	return []columnSpec{
		{"created_at_unix", "created_at_unix INTEGER DEFAULT 0"},
		{"request_id", "request_id TEXT"},
		{"endpoint", "endpoint TEXT NOT NULL DEFAULT ''"},
		{"method", "method TEXT NOT NULL DEFAULT ''"},
		{"status", "status INTEGER NOT NULL DEFAULT 0"},
		{"latency_ms", "latency_ms INTEGER NOT NULL DEFAULT 0"},
		{"ttfb_ms", "ttfb_ms INTEGER DEFAULT 0"},
		{"stream", "stream INTEGER NOT NULL DEFAULT 0"},
		{"client_ip_hash", "client_ip_hash TEXT"},
		{"api_key_hash", "api_key_hash TEXT"},
		{"model_requested", "model_requested TEXT"},
		{"model_returned", "model_returned TEXT"},
		{"input_tokens", "input_tokens INTEGER DEFAULT 0"},
		{"output_tokens", "output_tokens INTEGER DEFAULT 0"},
		{"reasoning_tokens", "reasoning_tokens INTEGER DEFAULT 0"},
		{"cached_tokens", "cached_tokens INTEGER DEFAULT 0"},
		{"total_tokens", "total_tokens INTEGER DEFAULT 0"},
		{"request_bytes", "request_bytes INTEGER DEFAULT 0"},
		{"response_bytes", "response_bytes INTEGER DEFAULT 0"},
		{"error", "error TEXT"},
	}
}

func healthMetricColumns() []columnSpec {
	return []columnSpec{
		{"timestamp_unix", "timestamp_unix INTEGER DEFAULT 0"},
		{"queue_depth", "queue_depth INTEGER DEFAULT 0"},
		{"dropped_events_total", "dropped_events_total INTEGER DEFAULT 0"},
		{"parse_error_total", "parse_error_total INTEGER DEFAULT 0"},
		{"db_write_error_total", "db_write_error_total INTEGER DEFAULT 0"},
	}
}

func ensureColumns(sqlDB *sql.DB, table string, cols []columnSpec) error {
	rows, err := sqlDB.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan %s columns: %w", table, err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s columns: %w", table, err)
	}

	for _, col := range cols {
		if existing[col.name] {
			continue
		}
		if _, err := sqlDB.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, col.ddl)); err != nil {
			return fmt.Errorf("add %s.%s: %w", table, col.name, err)
		}
	}
	return nil
}

func runBackfills(sqlDB *sql.DB) error {
	statements := []string{
		`UPDATE request_usage SET created_at_unix = COALESCE(CAST(strftime('%s', created_at) AS INTEGER), 0) WHERE created_at_unix IS NULL OR created_at_unix <= 0`,
		`UPDATE health_metrics SET timestamp_unix = COALESCE(CAST(strftime('%s', timestamp) AS INTEGER), 0) WHERE timestamp_unix IS NULL OR timestamp_unix <= 0`,
		`UPDATE request_usage SET endpoint = COALESCE(endpoint, '') WHERE endpoint IS NULL`,
		`UPDATE request_usage SET method = COALESCE(method, '') WHERE method IS NULL`,
		`UPDATE request_usage SET status = COALESCE(status, 0) WHERE status IS NULL`,
		`UPDATE request_usage SET latency_ms = COALESCE(latency_ms, 0) WHERE latency_ms IS NULL`,
		`UPDATE request_usage SET ttfb_ms = 0 WHERE ttfb_ms IS NULL OR ttfb_ms < 0`,
		`UPDATE request_usage SET stream = COALESCE(stream, 0) WHERE stream IS NULL`,
		`UPDATE request_usage SET input_tokens = COALESCE(input_tokens, 0) WHERE input_tokens IS NULL`,
		`UPDATE request_usage SET output_tokens = COALESCE(output_tokens, 0) WHERE output_tokens IS NULL`,
		`UPDATE request_usage SET reasoning_tokens = COALESCE(reasoning_tokens, 0) WHERE reasoning_tokens IS NULL`,
		`UPDATE request_usage SET cached_tokens = COALESCE(cached_tokens, 0) WHERE cached_tokens IS NULL`,
		`UPDATE request_usage SET total_tokens = COALESCE(total_tokens, 0) WHERE total_tokens IS NULL`,
		`UPDATE request_usage SET request_bytes = 0 WHERE request_bytes IS NULL OR request_bytes < 0`,
		`UPDATE request_usage SET response_bytes = 0 WHERE response_bytes IS NULL OR response_bytes < 0`,
		`UPDATE health_metrics SET queue_depth = COALESCE(queue_depth, 0) WHERE queue_depth IS NULL`,
		`UPDATE health_metrics SET dropped_events_total = COALESCE(dropped_events_total, 0) WHERE dropped_events_total IS NULL`,
		`UPDATE health_metrics SET parse_error_total = COALESCE(parse_error_total, 0) WHERE parse_error_total IS NULL`,
		`UPDATE health_metrics SET db_write_error_total = COALESCE(db_write_error_total, 0) WHERE db_write_error_total IS NULL`,
	}
	for _, stmt := range statements {
		if _, err := sqlDB.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

func (db *DB) InsertBatch(records []UsageRecord) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO request_usage (
			created_at, created_at_unix, request_id, endpoint, method, status, latency_ms, ttfb_ms, stream,
			client_ip_hash, api_key_hash, model_requested, model_returned,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			request_bytes, response_bytes, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	streamInt := func(s bool) int {
		if s {
			return 1
		}
		return 0
	}

	for _, r := range records {
		_, err := stmt.Exec(
			r.CreatedAt, unixFromTimestamp(r.CreatedAt), r.RequestID, r.Endpoint, r.Method, r.Status, r.LatencyMs, r.TTFBMs, streamInt(r.Stream),
			r.ClientIPHash, r.APIKeyHash, r.ModelRequested, r.ModelReturned,
			r.InputTokens, r.OutputTokens, r.ReasoningTokens, r.CachedTokens, r.TotalTokens,
			r.RequestBytes, r.ResponseBytes, r.Error,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (db *DB) InsertHealthMetric(ts string, queueDepth int, dropped, parseErrors, dbErrors int64) error {
	_, err := db.sql.Exec(`
		INSERT INTO health_metrics (timestamp, timestamp_unix, queue_depth, dropped_events_total, parse_error_total, db_write_error_total)
		VALUES (?, ?, ?, ?, ?, ?)
	`, ts, unixFromTimestamp(ts), queueDepth, dropped, parseErrors, dbErrors)
	return err
}

func unixFromTimestamp(ts string) int64 {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func (db *DB) Summary(since time.Time) (*SummaryRow, error) {
	row := &SummaryRow{}
	err := db.sql.QueryRow(`
		SELECT
			COUNT(*),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM request_usage WHERE created_at_unix >= ?
	`, since.Unix()).Scan(
		&row.TotalRequests, &row.FailedRequests,
		&row.TotalInputTokens, &row.TotalOutputTokens, &row.TotalReasoningTokens,
		&row.TotalCachedTokens, &row.TotalTokens,
	)
	return row, err
}

func (db *DB) Models(since time.Time) ([]ModelRow, error) {
	rows, err := db.sql.Query(`
		SELECT
			COALESCE(NULLIF(TRIM(model_returned), ''), 'unknown'),
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM request_usage WHERE created_at_unix >= ?
		GROUP BY COALESCE(NULLIF(TRIM(model_returned), ''), 'unknown') ORDER BY COUNT(*) DESC
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ModelRow
	for rows.Next() {
		var r ModelRow
		if err := rows.Scan(&r.Model, &r.RequestCount, &r.InputTokens, &r.OutputTokens, &r.ReasoningTokens, &r.CachedTokens, &r.TotalTokens); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) Keys(since time.Time) ([]KeyRow, error) {
	rows, err := db.sql.Query(`
		SELECT
			COALESCE(NULLIF(TRIM(api_key_hash), ''), 'unknown'),
			COUNT(*),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM request_usage WHERE created_at_unix >= ?
		GROUP BY COALESCE(NULLIF(TRIM(api_key_hash), ''), 'unknown') ORDER BY COUNT(*) DESC
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []KeyRow
	for rows.Next() {
		var r KeyRow
		if err := rows.Scan(&r.KeyHash, &r.RequestCount, &r.FailedCount, &r.InputTokens, &r.OutputTokens, &r.TotalTokens); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) Timeseries(since time.Time, bucketMin int) ([]TimeseriesRow, error) {
	if bucketMin <= 0 {
		bucketMin = 10
	}
	// Bucket by truncating created_at to the nearest bucketMin-minute boundary.
	// Use Unix epoch arithmetic: floor(unixepoch / (bucketMin*60)) * (bucketMin*60)
	// Then convert back to an ISO timestamp for display.
	bucketSec := int64(bucketMin) * 60
	bucketExpr := fmt.Sprintf(
		`strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', (created_at_unix / %d) * %d, 'unixepoch')`,
		bucketSec, bucketSec,
	)

	rows, err := db.sql.Query(`
		SELECT
			`+bucketExpr+`,
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM request_usage WHERE created_at_unix >= ?
		GROUP BY 1 ORDER BY 1 ASC
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TimeseriesRow
	for rows.Next() {
		var r TimeseriesRow
		if err := rows.Scan(&r.Timestamp, &r.Count, &r.InputTokens, &r.OutputTokens, &r.TotalTokens); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) Requests(limit int, statusMin, statusMax int, model, endpoint string, since time.Time) ([]RequestRow, error) {
	query := "SELECT id, COALESCE(created_at, ''), COALESCE(request_id, ''), COALESCE(endpoint, ''), COALESCE(method, ''), COALESCE(status, 0), COALESCE(latency_ms, 0), COALESCE(ttfb_ms, 0), COALESCE(stream, 0), COALESCE(client_ip_hash, ''), COALESCE(api_key_hash, ''), COALESCE(model_requested, ''), COALESCE(model_returned, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(reasoning_tokens, 0), COALESCE(cached_tokens, 0), COALESCE(total_tokens, 0), COALESCE(request_bytes, 0), COALESCE(response_bytes, 0), COALESCE(error, '') FROM request_usage WHERE 1=1"
	var args []any

	if !since.IsZero() {
		query += " AND created_at_unix >= ?"
		args = append(args, since.Unix())
	}
	if statusMin > 0 {
		query += " AND status >= ?"
		args = append(args, statusMin)
	}
	if statusMax > 0 {
		query += " AND status < ?"
		args = append(args, statusMax)
	}
	if model != "" {
		query += " AND model_returned = ?"
		args = append(args, model)
	}
	if endpoint != "" {
		query += " AND endpoint = ?"
		args = append(args, endpoint)
	}

	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RequestRow
	for rows.Next() {
		var r RequestRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.RequestID, &r.Endpoint, &r.Method, &r.Status, &r.LatencyMs, &r.TTFBMs, &r.Stream, &r.ClientIPHash, &r.APIKeyHash, &r.ModelRequested, &r.ModelReturned, &r.InputTokens, &r.OutputTokens, &r.ReasoningTokens, &r.CachedTokens, &r.TotalTokens, &r.RequestBytes, &r.ResponseBytes, &r.Error); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ErrorTimeline returns health_metrics snapshots over time (the real health data
// written by the periodic health reporter).
func (db *DB) ErrorTimeline(since time.Time) ([]ErrorTimelineRow, error) {
	rows, err := db.sql.Query(`
		SELECT
			timestamp,
			0,
			parse_error_total,
			db_write_error_total,
			dropped_events_total
		FROM health_metrics WHERE timestamp_unix >= ?
		ORDER BY timestamp ASC
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ErrorTimelineRow
	for rows.Next() {
		var r ErrorTimelineRow
		if err := rows.Scan(&r.Timestamp, &r.Count, &r.ParseErrors, &r.DBErrors, &r.DroppedEvents); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// LatestHealth returns the most recent health_metrics snapshot.
func (db *DB) LatestHealth() (*HealthRow, error) {
	row := &HealthRow{}
	err := db.sql.QueryRow(`
		SELECT COALESCE(timestamp, ''),
			COALESCE(queue_depth, 0),
			COALESCE(dropped_events_total, 0),
			COALESCE(parse_error_total, 0),
			COALESCE(db_write_error_total, 0)
		FROM health_metrics ORDER BY id DESC LIMIT 1
	`).Scan(&row.Timestamp, &row.QueueDepth, &row.DroppedEvents, &row.ParseErrors, &row.DBErrors)
	if err == sql.ErrNoRows {
		return row, nil
	}
	return row, err
}

// ErrorTimelineFromRequests returns a timeline of non-2xx status codes (for API compatibility).
func (db *DB) ErrorTimelineFromRequests(since time.Time) ([]ErrorTimelineRow, error) {
	rows, err := db.sql.Query(`
		SELECT
			strftime('%Y-%m-%dT%H:00:00Z', created_at_unix, 'unixepoch'),
			COUNT(*),
			0,
			0,
			0
		FROM request_usage WHERE created_at_unix >= ? AND status >= 400
		GROUP BY 1 ORDER BY 1 ASC
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ErrorTimelineRow
	for rows.Next() {
		var r ErrorTimelineRow
		if err := rows.Scan(&r.Timestamp, &r.Count, &r.ParseErrors, &r.DBErrors, &r.DroppedEvents); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
