package db

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type UsageRecord struct {
	CreatedAt           string
	RequestID           string
	Endpoint            string
	Method              string
	Status              int
	LatencyMs           int64
	TTFBMs              int64
	Stream              bool
	ClientIPHash        string
	APIKeyHash          string
	ModelRequested      string
	ModelReturned       string
	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CachedTokens        int64
	CacheCreationTokens int64
	TotalTokens         int64
	RequestBytes        int64
	ResponseBytes       int64
	Error               string

	// W3 extended fields
	EndpointProfile   string
	CaptureMode       string
	MeteringKind      string
	UsageRawJSON      string
	UsageRawTruncated bool
	BillableInput     float64
	BillableOutput    float64
	BillableTotal     float64
	BillableUnit      string
	CaptureOutcome    string
	CaptureReason     string

	ErrorClass            string
	ErrorType             string
	ErrorCode             string
	ErrorParam            string
	ErrorMessage          string
	ErrorMessageTruncated bool

	ModelReturnedSource string
	UsageSource         string
	TerminalEvent       string
	TerminalReason      string
	SideUsageEventID    int64
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
	Model                     string           `json:"model"`
	ModelSource               string           `json:"model_source"`
	RequestCount              int64            `json:"request_count"`
	FailedCount               int64            `json:"failed_count"`
	InputTokens               int64            `json:"input_tokens"`
	OutputTokens              int64            `json:"output_tokens"`
	ReasoningTokens           int64            `json:"reasoning_tokens"`
	CachedTokens              int64            `json:"cached_tokens"`
	CacheCreationTokens       int64            `json:"cache_creation_tokens"`
	TotalTokens               int64            `json:"total_tokens"`
	ModelReturnedSourceCounts map[string]int64 `json:"model_returned_source_counts,omitempty"`
	UsageSourceCounts         map[string]int64 `json:"usage_source_counts,omitempty"`
	MissingUsageCount         int64            `json:"missing_usage_count"`
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
	Timestamp           string `json:"timestamp"`
	Count               int64  `json:"count"`
	FailedCount         int64  `json:"failed_count"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
	CachedTokens        int64  `json:"cached_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	TotalTokens         int64  `json:"total_tokens"`
	AvgLatencyMs        int64  `json:"avg_latency_ms"`
	AvgTTFBMs           int64  `json:"avg_ttfb_ms"`
}

type ModelTimeseriesRow struct {
	Timestamp           string `json:"timestamp"`
	Model               string `json:"model"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
	CachedTokens        int64  `json:"cached_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
}

type ActivityRow struct {
	SampleSize          int64   `json:"sample_size"`
	SuccessCount        int64   `json:"success_count"`
	FailedCount         int64   `json:"failed_count"`
	FailureRate         float64 `json:"failure_rate"`
	AvgLatencyMs        int64   `json:"avg_latency_ms"`
	P95LatencyMs        int64   `json:"p95_latency_ms"`
	AvgTTFBMs           int64   `json:"avg_ttfb_ms"`
	P95TTFBMs           int64   `json:"p95_ttfb_ms"`
	CaptureCaptured     int64   `json:"capture_captured"`
	CaptureFailed       int64   `json:"capture_failed"`
	CaptureSkipped      int64   `json:"capture_skipped"`
	LatestErrorStatus   int     `json:"latest_error_status"`
	LatestErrorAt       string  `json:"latest_error_at"`
	LatestError         string  `json:"latest_error"`
	LatestErrorEndpoint string  `json:"latest_error_endpoint"`
	LatestErrorModel    string  `json:"latest_error_model"`
}

type RequestRow struct {
	ID                    int64  `json:"id"`
	CreatedAt             string `json:"created_at"`
	RequestID             string `json:"request_id"`
	Endpoint              string `json:"endpoint"`
	Method                string `json:"method"`
	Status                int    `json:"status"`
	LatencyMs             int64  `json:"latency_ms"`
	TTFBMs                int64  `json:"ttfb_ms"`
	Stream                bool   `json:"stream"`
	ClientIPHash          string `json:"client_ip_hash"`
	APIKeyHash            string `json:"api_key_hash"`
	ModelRequested        string `json:"model_requested"`
	ModelReturned         string `json:"model_returned"`
	InputTokens           int64  `json:"input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	ReasoningTokens       int64  `json:"reasoning_tokens"`
	CachedTokens          int64  `json:"cached_tokens"`
	CacheCreationTokens   int64  `json:"cache_creation_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	RequestBytes          int64  `json:"request_bytes"`
	ResponseBytes         int64  `json:"response_bytes"`
	Error                 string `json:"error"`
	EndpointProfile       string `json:"endpoint_profile"`
	CaptureMode           string `json:"capture_mode"`
	MeteringKind          string `json:"metering_kind"`
	CaptureOutcome        string `json:"capture_outcome"`
	CaptureReason         string `json:"capture_reason"`
	ErrorClass            string `json:"error_class"`
	ErrorType             string `json:"error_type"`
	ErrorCode             string `json:"error_code"`
	ErrorParam            string `json:"error_param"`
	ErrorMessage          string `json:"error_message"`
	ErrorMessageTruncated bool   `json:"error_message_truncated"`
	ModelReturnedSource   string `json:"model_returned_source"`
	UsageSource           string `json:"usage_source"`
	TerminalEvent         string `json:"terminal_event"`
	TerminalReason        string `json:"terminal_reason"`
	SideUsageEventID      int64  `json:"side_usage_event_id"`
}

type HealthRow struct {
	Timestamp     string `json:"timestamp"`
	QueueDepth    int64  `json:"queue_depth"`
	DroppedEvents int64  `json:"dropped_events"`
	ParseErrors   int64  `json:"parse_errors"`
	DBErrors      int64  `json:"db_errors"`
	SSELineSkips  int64  `json:"sse_line_skips"`
}

type ErrorTimelineRow struct {
	Timestamp     string `json:"timestamp"`
	Count         int64  `json:"count"`
	ParseErrors   int64  `json:"parse_errors"`
	DBErrors      int64  `json:"db_errors"`
	DroppedEvents int64  `json:"dropped_events"`
}

type OverviewSection struct {
	Data  interface{} `json:"data"`
	Error string      `json:"error"`
}

type OverviewRow struct {
	Range    string          `json:"range"`
	Selected OverviewSection `json:"selected"`
	Recent1h OverviewSection `json:"recent_1h"`
	Capture  OverviewSection `json:"capture"`
	Cost     OverviewSection `json:"cost"`
}

type IssueRow struct {
	Class       string `json:"class"`
	Label       string `json:"label"`
	Count       int64  `json:"count"`
	Severity    string `json:"severity"`
	SourceGroup string `json:"source_group"`
	LatestAt    string `json:"latest_at"`
	Status      int    `json:"status"`
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	ModelSource string `json:"model_source"`
	APIKeyHash  string `json:"api_key_hash"`
	ErrorType   string `json:"error_type"`
	ErrorCode   string `json:"error_code"`
	Message     string `json:"message"`
	RequestID   string `json:"request_id"`
}

type SideUsageEvent struct {
	ID                    int64  `json:"id"`
	ReceivedAt            string `json:"received_at"`
	ReceivedAtUnix        int64  `json:"received_at_unix"`
	RequestID             string `json:"request_id"`
	MatchedRequestUsageID int64  `json:"matched_request_usage_id"`
	MatchStatus           string `json:"match_status"`
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	Alias                 string `json:"alias"`
	Endpoint              string `json:"endpoint"`
	AuthType              string `json:"auth_type"`
	AuthIndexHash         string `json:"auth_index_hash"`
	SourceHash            string `json:"source_hash"`
	APIKeyHash            string `json:"api_key_hash"`
	InputTokens           int64  `json:"input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	ReasoningTokens       int64  `json:"reasoning_tokens"`
	CachedTokens          int64  `json:"cached_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	LatencyMs             int64  `json:"latency_ms"`
	Failed                int64  `json:"failed"`
	ErrorClass            string `json:"error_class"`
}

type CredentialHealthRow struct {
	Provider       string `json:"provider"`
	CredentialHash string `json:"credential_hash"`
	AuthIndexHash  string `json:"auth_index_hash"`
	LabelHash      string `json:"label_hash"`
	Status         string `json:"status"`
	SuccessCount   int64  `json:"success_count"`
	FailedCount    int64  `json:"failed_count"`
	CheckedAt      string `json:"checked_at"`
	CheckedAtUnix  int64  `json:"checked_at_unix"`
	ErrorClass     string `json:"error_class"`
}

type QuotaCurrentRow struct {
	Provider        string  `json:"provider"`
	CredentialHash  string  `json:"credential_hash"`
	WindowKey       string  `json:"window_key"`
	ExpiresAt       string  `json:"expires_at"`
	ExpiresAtUnix   int64   `json:"expires_at_unix"`
	CheckedAt       string  `json:"checked_at"`
	CheckedAtUnix   int64   `json:"checked_at_unix"`
	Plan            string  `json:"plan"`
	LimitAmount     float64 `json:"limit_amount"`
	RemainingAmount float64 `json:"remaining_amount"`
	UsedAmount      float64 `json:"used_amount"`
	Unit            string  `json:"unit"`
	ResetAt         string  `json:"reset_at"`
	ResetAtUnix     int64   `json:"reset_at_unix"`
	Status          string  `json:"status"`
	QuotaSupported  int64   `json:"quota_supported"`
	AdapterStatus   string  `json:"adapter_status"`
	ErrorClass      string  `json:"error_class"`
	ErrorMessage    string  `json:"error_message"`
	Partial         int64   `json:"partial"`
}

type QuotaRefreshEventRow struct {
	ID             int64  `json:"id"`
	CheckedAt      string `json:"checked_at"`
	CheckedAtUnix  int64  `json:"checked_at_unix"`
	Provider       string `json:"provider"`
	CredentialHash string `json:"credential_hash"`
	Phase          string `json:"phase"`
	Status         string `json:"status"`
	AdapterStatus  string `json:"adapter_status"`
	DurationMs     int64  `json:"duration_ms"`
	ErrorClass     string `json:"error_class"`
	ErrorMessage   string `json:"error_message"`
	Partial        int64  `json:"partial"`
}

type DB struct {
	sql  *sql.DB
	read *sql.DB
	path string
}

// effectiveModelExpr returns the best available model name for grouping.
// Falls back: model_returned -> model_requested -> "unidentified".
const effectiveModelExpr = `COALESCE(NULLIF(TRIM(model_returned), ''), NULLIF(TRIM(model_requested), ''), 'unidentified')`

// modelSourceExpr returns the source of the model for a single row.
const modelSourceExpr = `CASE WHEN NULLIF(TRIM(model_returned), '') IS NOT NULL THEN 'returned' WHEN NULLIF(TRIM(model_requested), '') IS NOT NULL THEN 'requested' ELSE 'unidentified' END`

// modelSourceAggExpr returns the dominant source across an aggregate group.
const modelSourceAggExpr = `CASE WHEN SUM(CASE WHEN NULLIF(TRIM(model_returned), '') IS NOT NULL THEN 1 ELSE 0 END) > 0 THEN 'returned' WHEN SUM(CASE WHEN NULLIF(TRIM(model_requested), '') IS NOT NULL THEN 1 ELSE 0 END) > 0 THEN 'requested' ELSE 'unidentified' END`

const modelReturnedSourceCompatExpr = `CASE WHEN NULLIF(TRIM(model_returned_source), '') IS NOT NULL THEN model_returned_source WHEN NULLIF(TRIM(model_returned), '') IS NOT NULL THEN 'legacy' ELSE 'none' END`

const usageSourceCompatExpr = `CASE WHEN NULLIF(TRIM(usage_source), '') IS NOT NULL THEN usage_source WHEN COALESCE(input_tokens, 0) > 0 OR COALESCE(output_tokens, 0) > 0 OR COALESCE(reasoning_tokens, 0) > 0 OR COALESCE(cached_tokens, 0) > 0 OR COALESCE(total_tokens, 0) > 0 THEN 'http_response' ELSE 'none' END`

const schemaVersion = 6
const activitySampleLimit = 1000

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

	if err := os.Chmod(path, 0600); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("chmod db: %w", err)
	}

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

	readDB, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	readDB.SetMaxOpenConns(1)
	readDB.SetMaxIdleConns(1)
	if _, err := readDB.Exec("PRAGMA query_only = ON"); err != nil {
		readDB.Close()
		sqlDB.Close()
		return nil, fmt.Errorf("configure read db: %w", err)
	}

	return &DB{sql: sqlDB, read: readDB, path: path}, nil
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
			db_write_error_total INTEGER DEFAULT 0,
			sse_line_skips_total INTEGER DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			applied_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS schema_tasks (
			name TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
		`
	if _, err := sqlDB.Exec(schema); err != nil {
		return err
	}
	// Ensure name column exists on legacy schema_migrations tables.
	if err := ensureColumns(sqlDB, "schema_migrations", []columnSpec{
		{"name", "name TEXT NOT NULL DEFAULT ''"},
	}); err != nil {
		return err
	}
	if err := ensureColumns(sqlDB, "request_usage", requestUsageColumns()); err != nil {
		return err
	}
	if err := ensureColumns(sqlDB, "health_metrics", healthMetricColumns()); err != nil {
		return err
	}
	if err := createTablesV6(sqlDB); err != nil {
		return err
	}
	if err := createIndexes(sqlDB); err != nil {
		return err
	}
	if err := runSchemaTask(sqlDB, "backfill_v4_normalization", backfillV4Normalization); err != nil {
		return err
	}
	if _, err := sqlDB.Exec(`
			INSERT OR IGNORE INTO schema_migrations (version, name, applied_at)
			VALUES (?, ?, ?)
		`, schemaVersion, "v6_observability_fields", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	_, err := sqlDB.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion))
	return err
}

func createTablesV6(sqlDB *sql.DB) error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS side_usage_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			received_at TEXT NOT NULL,
			received_at_unix INTEGER NOT NULL DEFAULT 0,
			request_id TEXT NOT NULL DEFAULT '',
			matched_request_usage_id INTEGER NOT NULL DEFAULT 0,
			match_status TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			alias TEXT NOT NULL DEFAULT '',
			endpoint TEXT NOT NULL DEFAULT '',
			auth_type TEXT NOT NULL DEFAULT '',
			auth_index_hash TEXT NOT NULL DEFAULT '',
			source_hash TEXT NOT NULL DEFAULT '',
			api_key_hash TEXT NOT NULL DEFAULT '',
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			reasoning_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			failed INTEGER NOT NULL DEFAULT 0,
			error_class TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS credential_health (
			provider TEXT NOT NULL DEFAULT '',
			credential_hash TEXT NOT NULL DEFAULT '',
			auth_index_hash TEXT NOT NULL DEFAULT '',
			label_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			success_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0,
			checked_at TEXT NOT NULL,
			checked_at_unix INTEGER NOT NULL DEFAULT 0,
			error_class TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (provider, credential_hash)
		)`,
		`CREATE TABLE IF NOT EXISTS quota_current (
			provider TEXT NOT NULL DEFAULT '',
			credential_hash TEXT NOT NULL DEFAULT '',
			window_key TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL DEFAULT '',
			expires_at_unix INTEGER NOT NULL DEFAULT 0,
			checked_at TEXT NOT NULL,
			checked_at_unix INTEGER NOT NULL DEFAULT 0,
			plan TEXT NOT NULL DEFAULT '',
			limit_amount REAL NOT NULL DEFAULT 0,
			remaining_amount REAL NOT NULL DEFAULT 0,
			used_amount REAL NOT NULL DEFAULT 0,
			unit TEXT NOT NULL DEFAULT '',
			reset_at TEXT NOT NULL DEFAULT '',
			reset_at_unix INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT '',
			quota_supported INTEGER NOT NULL DEFAULT 0,
			adapter_status TEXT NOT NULL DEFAULT '',
			error_class TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			partial INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (provider, credential_hash, window_key)
		)`,
		`CREATE TABLE IF NOT EXISTS quota_refresh_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			checked_at TEXT NOT NULL,
			checked_at_unix INTEGER NOT NULL DEFAULT 0,
			provider TEXT NOT NULL DEFAULT '',
			credential_hash TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			adapter_status TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			error_class TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			partial INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, ddl := range tables {
		if _, err := sqlDB.Exec(ddl); err != nil {
			return err
		}
	}
	return nil
}

func createIndexes(sqlDB *sql.DB) error {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_request_usage_created_at ON request_usage(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_created_at_unix ON request_usage(created_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_model ON request_usage(model_returned)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_key ON request_usage(api_key_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_status ON request_usage(status)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_endpoint_profile ON request_usage(endpoint_profile)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_capture_outcome ON request_usage(capture_outcome)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_status_created_at_unix ON request_usage(status, created_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_error_class_created_at_unix ON request_usage(error_class, created_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_model_requested_created_at_unix ON request_usage(model_requested, created_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_model_returned_created_at_unix ON request_usage(model_returned, created_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_usage_source_created_at_unix ON request_usage(usage_source, created_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_capture_reason_created_at_unix ON request_usage(capture_reason, created_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_request_usage_terminal_event_created_at_unix ON request_usage(terminal_event, created_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_health_metrics_timestamp ON health_metrics(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_health_metrics_timestamp_unix ON health_metrics(timestamp_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_side_usage_events_request_id ON side_usage_events(request_id)`,
		`CREATE INDEX IF NOT EXISTS idx_side_usage_events_matched_request_usage_id ON side_usage_events(matched_request_usage_id)`,
		`CREATE INDEX IF NOT EXISTS idx_side_usage_events_match_status_received_at_unix ON side_usage_events(match_status, received_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_health_provider_status ON credential_health(provider, status)`,
		`CREATE INDEX IF NOT EXISTS idx_quota_current_provider_status ON quota_current(provider, status)`,
		`CREATE INDEX IF NOT EXISTS idx_quota_refresh_events_provider_checked_at_unix ON quota_refresh_events(provider, checked_at_unix)`,
		`CREATE INDEX IF NOT EXISTS idx_quota_refresh_events_status_checked_at_unix ON quota_refresh_events(status, checked_at_unix)`,
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
		{"cache_creation_tokens", "cache_creation_tokens INTEGER DEFAULT 0"},
		{"total_tokens", "total_tokens INTEGER DEFAULT 0"},
		{"request_bytes", "request_bytes INTEGER DEFAULT 0"},
		{"response_bytes", "response_bytes INTEGER DEFAULT 0"},
		{"error", "error TEXT"},
		{"endpoint_profile", "endpoint_profile TEXT DEFAULT ''"},
		{"capture_mode", "capture_mode TEXT DEFAULT ''"},
		{"metering_kind", "metering_kind TEXT DEFAULT ''"},
		{"usage_raw_json", "usage_raw_json TEXT DEFAULT ''"},
		{"usage_raw_truncated", "usage_raw_truncated INTEGER DEFAULT 0"},
		{"billable_input", "billable_input REAL DEFAULT 0.0"},
		{"billable_output", "billable_output REAL DEFAULT 0.0"},
		{"billable_total", "billable_total REAL DEFAULT 0.0"},
		{"billable_unit", "billable_unit TEXT DEFAULT ''"},
		{"capture_outcome", "capture_outcome TEXT DEFAULT ''"},
		{"capture_reason", "capture_reason TEXT DEFAULT ''"},
		{"error_class", "error_class TEXT DEFAULT ''"},
		{"error_type", "error_type TEXT DEFAULT ''"},
		{"error_code", "error_code TEXT DEFAULT ''"},
		{"error_param", "error_param TEXT DEFAULT ''"},
		{"error_message", "error_message TEXT DEFAULT ''"},
		{"error_message_truncated", "error_message_truncated INTEGER DEFAULT 0"},
		{"model_returned_source", "model_returned_source TEXT DEFAULT ''"},
		{"usage_source", "usage_source TEXT DEFAULT ''"},
		{"terminal_event", "terminal_event TEXT DEFAULT ''"},
		{"terminal_reason", "terminal_reason TEXT DEFAULT ''"},
		{"side_usage_event_id", "side_usage_event_id INTEGER DEFAULT 0"},
	}
}

func healthMetricColumns() []columnSpec {
	return []columnSpec{
		{"timestamp_unix", "timestamp_unix INTEGER DEFAULT 0"},
		{"queue_depth", "queue_depth INTEGER DEFAULT 0"},
		{"dropped_events_total", "dropped_events_total INTEGER DEFAULT 0"},
		{"parse_error_total", "parse_error_total INTEGER DEFAULT 0"},
		{"db_write_error_total", "db_write_error_total INTEGER DEFAULT 0"},
		{"sse_line_skips_total", "sse_line_skips_total INTEGER DEFAULT 0"},
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

func runSchemaTask(sqlDB *sql.DB, name string, fn func(*sql.Tx) error) error {
	tx, err := sqlDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existing string
	err = tx.QueryRow(
		"SELECT name FROM schema_tasks WHERE name = ?",
		name,
	).Scan(&existing)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"INSERT INTO schema_tasks (name, applied_at) VALUES (?, ?)",
		name,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func backfillV4Normalization(tx *sql.Tx) error {
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
		`UPDATE request_usage SET cache_creation_tokens = COALESCE(cache_creation_tokens, 0) WHERE cache_creation_tokens IS NULL`,
		`UPDATE request_usage SET total_tokens = COALESCE(total_tokens, 0) WHERE total_tokens IS NULL`,
		`UPDATE request_usage SET request_bytes = 0 WHERE request_bytes IS NULL OR request_bytes < 0`,
		`UPDATE request_usage SET response_bytes = 0 WHERE response_bytes IS NULL OR response_bytes < 0`,
		`UPDATE health_metrics SET queue_depth = COALESCE(queue_depth, 0) WHERE queue_depth IS NULL`,
		`UPDATE health_metrics SET dropped_events_total = COALESCE(dropped_events_total, 0) WHERE dropped_events_total IS NULL`,
		`UPDATE health_metrics SET parse_error_total = COALESCE(parse_error_total, 0) WHERE parse_error_total IS NULL`,
		`UPDATE health_metrics SET db_write_error_total = COALESCE(db_write_error_total, 0) WHERE db_write_error_total IS NULL`,
		`UPDATE health_metrics SET sse_line_skips_total = COALESCE(sse_line_skips_total, 0) WHERE sse_line_skips_total IS NULL`,
		// W3: Normalize new columns
		`UPDATE request_usage SET endpoint_profile = COALESCE(endpoint_profile, '') WHERE endpoint_profile IS NULL`,
		`UPDATE request_usage SET capture_mode = COALESCE(capture_mode, '') WHERE capture_mode IS NULL`,
		`UPDATE request_usage SET metering_kind = COALESCE(metering_kind, '') WHERE metering_kind IS NULL`,
		`UPDATE request_usage SET usage_raw_json = COALESCE(usage_raw_json, '') WHERE usage_raw_json IS NULL`,
		`UPDATE request_usage SET usage_raw_truncated = COALESCE(usage_raw_truncated, 0) WHERE usage_raw_truncated IS NULL`,
		`UPDATE request_usage SET billable_input = COALESCE(billable_input, 0.0) WHERE billable_input IS NULL`,
		`UPDATE request_usage SET billable_output = COALESCE(billable_output, 0.0) WHERE billable_output IS NULL`,
		`UPDATE request_usage SET billable_total = COALESCE(billable_total, 0.0) WHERE billable_total IS NULL`,
		`UPDATE request_usage SET billable_unit = COALESCE(billable_unit, '') WHERE billable_unit IS NULL`,
		`UPDATE request_usage SET capture_outcome = COALESCE(capture_outcome, '') WHERE capture_outcome IS NULL`,
		`UPDATE request_usage SET capture_reason = COALESCE(capture_reason, '') WHERE capture_reason IS NULL`,
		// V5: Normalize new error columns.
		`UPDATE request_usage SET error_class = COALESCE(error_class, '') WHERE error_class IS NULL`,
		`UPDATE request_usage SET error_type = COALESCE(error_type, '') WHERE error_type IS NULL`,
		`UPDATE request_usage SET error_code = COALESCE(error_code, '') WHERE error_code IS NULL`,
		`UPDATE request_usage SET error_param = COALESCE(error_param, '') WHERE error_param IS NULL`,
		`UPDATE request_usage SET error_message = COALESCE(error_message, '') WHERE error_message IS NULL`,
		`UPDATE request_usage SET error_message_truncated = COALESCE(error_message_truncated, 0) WHERE error_message_truncated IS NULL`,
		// V6: Normalize new observability columns.
		`UPDATE request_usage SET model_returned_source = COALESCE(model_returned_source, '') WHERE model_returned_source IS NULL`,
		`UPDATE request_usage SET usage_source = COALESCE(usage_source, '') WHERE usage_source IS NULL`,
		`UPDATE request_usage SET terminal_event = COALESCE(terminal_event, '') WHERE terminal_event IS NULL`,
		`UPDATE request_usage SET terminal_reason = COALESCE(terminal_reason, '') WHERE terminal_reason IS NULL`,
		`UPDATE request_usage SET side_usage_event_id = COALESCE(side_usage_event_id, 0) WHERE side_usage_event_id IS NULL`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Close() error {
	var err error
	if db.read != nil {
		err = db.read.Close()
	}
	if db.sql != nil {
		if closeErr := db.sql.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// WALCheckpointResult holds results from a WAL checkpoint PRAGMA.
type WALCheckpointResult struct {
	Busy         int
	LogFrames    int
	Checkpointed int
}

// CheckpointWAL runs a WAL checkpoint with the given mode (PASSIVE or TRUNCATE).
func (db *DB) CheckpointWAL(mode string) (WALCheckpointResult, error) {
	pragma := "PRAGMA wal_checkpoint(PASSIVE)"
	switch mode {
	case "", "PASSIVE":
		pragma = "PRAGMA wal_checkpoint(PASSIVE)"
	case "TRUNCATE":
		pragma = "PRAGMA wal_checkpoint(TRUNCATE)"
	default:
		return WALCheckpointResult{}, fmt.Errorf("unsupported wal checkpoint mode: %s", mode)
	}
	var r WALCheckpointResult
	err := db.sql.QueryRow(pragma).Scan(&r.Busy, &r.LogFrames, &r.Checkpointed)
	return r, err
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
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_creation_tokens, total_tokens,
			request_bytes, response_bytes, error,
			endpoint_profile, capture_mode, metering_kind,
			usage_raw_json, usage_raw_truncated,
			billable_input, billable_output, billable_total, billable_unit,
			capture_outcome, capture_reason,
				error_class, error_type, error_code, error_param, error_message, error_message_truncated,
				model_returned_source, usage_source, terminal_event, terminal_reason, side_usage_event_id
		) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?16, ?17, ?18, ?19, ?20, ?21,
		          ?22, ?23, ?24, ?25, ?26, ?27, ?28, ?29, ?30, ?31, ?32,
		          ?33, ?34, ?35, ?36, ?37, ?38, ?39,
		          ?40, ?41, ?42, ?43, ?44)
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
	truncatedInt := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}

	for _, r := range records {
		createdAtUnix, err := unixFromTimestamp(r.CreatedAt)
		if err != nil {
			return fmt.Errorf("parse usage timestamp %q: %w", r.CreatedAt, err)
		}
		_, err = stmt.Exec(
			r.CreatedAt, createdAtUnix, r.RequestID, r.Endpoint, r.Method, r.Status, r.LatencyMs, r.TTFBMs, streamInt(r.Stream),
			r.ClientIPHash, r.APIKeyHash, r.ModelRequested, r.ModelReturned,
			r.InputTokens, r.OutputTokens, r.ReasoningTokens, r.CachedTokens, r.CacheCreationTokens, r.TotalTokens,
			r.RequestBytes, r.ResponseBytes, r.Error,
			r.EndpointProfile, r.CaptureMode, r.MeteringKind,
			r.UsageRawJSON, truncatedInt(r.UsageRawTruncated),
			r.BillableInput, r.BillableOutput, r.BillableTotal, r.BillableUnit,
			r.CaptureOutcome, r.CaptureReason,
			r.ErrorClass, r.ErrorType, r.ErrorCode, r.ErrorParam, r.ErrorMessage, truncatedInt(r.ErrorMessageTruncated),
			r.ModelReturnedSource, r.UsageSource, r.TerminalEvent, r.TerminalReason, r.SideUsageEventID,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (db *DB) InsertHealthMetric(ts string, queueDepth int, dropped, parseErrors, dbErrors, sseLineSkips int64) error {
	timestampUnix, err := unixFromTimestamp(ts)
	if err != nil {
		return fmt.Errorf("parse health timestamp %q: %w", ts, err)
	}
	_, err = db.sql.Exec(`
		INSERT INTO health_metrics (timestamp, timestamp_unix, queue_depth, dropped_events_total, parse_error_total, db_write_error_total, sse_line_skips_total)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, ts, timestampUnix, queueDepth, dropped, parseErrors, dbErrors, sseLineSkips)
	return err
}

func unixFromTimestamp(ts string) (int64, error) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

func (db *DB) Summary(since time.Time) (*SummaryRow, error) {
	row := &SummaryRow{}
	err := db.read.QueryRow(`
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
	rows, err := db.read.Query(`
		SELECT
			`+effectiveModelExpr+`,
			`+modelSourceAggExpr+`,
			COUNT(*),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COUNT(CASE WHEN capture_outcome = 'captured' AND (`+modelReturnedSourceCompatExpr+`) != 'none' THEN 1 END),
			COUNT(CASE WHEN capture_outcome NOT IN ('captured', '') THEN 1 END)
		FROM request_usage WHERE created_at_unix >= ?
		GROUP BY 1 ORDER BY COUNT(*) DESC
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ModelRow
	for rows.Next() {
		var r ModelRow
		var capturedWithSource int64
		var missingCount int64
		if err := rows.Scan(&r.Model, &r.ModelSource, &r.RequestCount, &r.FailedCount, &r.InputTokens, &r.OutputTokens, &r.ReasoningTokens, &r.CachedTokens, &r.CacheCreationTokens, &r.TotalTokens, &capturedWithSource, &missingCount); err != nil {
			return nil, err
		}
		r.MissingUsageCount = missingCount
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) ModelSourceCounts(since time.Time, model string) (modelReturnedSourceCounts map[string]int64, usageSourceCounts map[string]int64, err error) {
	modelReturnedSourceCounts = make(map[string]int64)
	usageSourceCounts = make(map[string]int64)
	modelRows, err := db.read.Query(`
		SELECT
			`+modelReturnedSourceCompatExpr+`,
			COUNT(*)
		FROM request_usage
		WHERE created_at_unix >= ? AND `+effectiveModelExpr+` = ?
		GROUP BY 1
	`, since.Unix(), model)
	if err != nil {
		return nil, nil, err
	}
	defer modelRows.Close()
	for modelRows.Next() {
		var src string
		var cnt int64
		if err := modelRows.Scan(&src, &cnt); err != nil {
			return nil, nil, err
		}
		modelReturnedSourceCounts[src] = cnt
	}

	usageRows, err := db.read.Query(`
		SELECT
			`+usageSourceCompatExpr+`,
			COUNT(*)
		FROM request_usage
		WHERE created_at_unix >= ? AND `+effectiveModelExpr+` = ?
		GROUP BY 1
	`, since.Unix(), model)
	if err != nil {
		return nil, nil, err
	}
	defer usageRows.Close()
	for usageRows.Next() {
		var src string
		var cnt int64
		if err := usageRows.Scan(&src, &cnt); err != nil {
			return nil, nil, err
		}
		usageSourceCounts[src] = cnt
	}
	return modelReturnedSourceCounts, usageSourceCounts, nil
}

func (db *DB) Keys(since time.Time) ([]KeyRow, error) {
	rows, err := db.read.Query(`
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
	bucketSec := int64(bucketMin) * 60
	bucketExpr := fmt.Sprintf(
		`strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', (created_at_unix / %d) * %d, 'unixepoch')`,
		bucketSec, bucketSec,
	)

	rows, err := db.read.Query(`
		SELECT
			`+bucketExpr+`,
			COUNT(*),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(CAST(ROUND(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END)) AS INTEGER), 0),
			COALESCE(CAST(ROUND(AVG(CASE WHEN ttfb_ms > 0 THEN ttfb_ms END)) AS INTEGER), 0)
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
		if err := rows.Scan(
			&r.Timestamp, &r.Count, &r.FailedCount,
			&r.InputTokens, &r.OutputTokens, &r.ReasoningTokens, &r.CachedTokens, &r.CacheCreationTokens, &r.TotalTokens,
			&r.AvgLatencyMs, &r.AvgTTFBMs,
		); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) ModelTimeseries(since time.Time, bucketMin int) ([]ModelTimeseriesRow, error) {
	if bucketMin <= 0 {
		bucketMin = 10
	}
	bucketSec := int64(bucketMin) * 60
	bucketExpr := fmt.Sprintf(
		`strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', (created_at_unix / %d) * %d, 'unixepoch')`,
		bucketSec, bucketSec,
	)

	rows, err := db.read.Query(`
		SELECT
			`+bucketExpr+`,
			`+effectiveModelExpr+`,
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0)
		FROM request_usage WHERE created_at_unix >= ?
		GROUP BY 1, 2 ORDER BY 1 ASC, COUNT(*) DESC
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ModelTimeseriesRow
	for rows.Next() {
		var r ModelTimeseriesRow
		if err := rows.Scan(
			&r.Timestamp, &r.Model,
			&r.InputTokens, &r.OutputTokens, &r.ReasoningTokens, &r.CachedTokens, &r.CacheCreationTokens,
		); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) Activity(since time.Time) (*ActivityRow, error) {
	row := &ActivityRow{}
	err := db.read.QueryRow(`
		WITH sampled AS (
			SELECT *
			FROM request_usage
			WHERE created_at_unix >= ?
			ORDER BY id DESC
			LIMIT ?
		)
		SELECT
			COUNT(*),
			COUNT(CASE WHEN status < 400 THEN 1 END),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(CAST(ROUND(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END)) AS INTEGER), 0),
			COALESCE(CAST(ROUND(AVG(CASE WHEN ttfb_ms > 0 THEN ttfb_ms END)) AS INTEGER), 0),
			COUNT(CASE WHEN capture_outcome = 'captured' THEN 1 END),
			COUNT(CASE WHEN capture_outcome = 'failed' THEN 1 END),
			COUNT(CASE WHEN capture_outcome = 'skipped' THEN 1 END)
		FROM sampled
	`, since.Unix(), activitySampleLimit).Scan(
		&row.SampleSize, &row.SuccessCount, &row.FailedCount,
		&row.AvgLatencyMs, &row.AvgTTFBMs,
		&row.CaptureCaptured, &row.CaptureFailed, &row.CaptureSkipped,
	)
	if err != nil {
		return nil, err
	}
	if row.SampleSize > 0 {
		row.FailureRate = float64(row.FailedCount) / float64(row.SampleSize)
	}

	var perr error
	row.P95LatencyMs, perr = db.percentileInt(since, "latency_ms", 0.95, activitySampleLimit)
	if perr != nil {
		return nil, perr
	}
	row.P95TTFBMs, perr = db.percentileInt(since, "ttfb_ms", 0.95, activitySampleLimit)
	if perr != nil {
		return nil, perr
	}

	err = db.read.QueryRow(`
		WITH sampled AS (
			SELECT *
			FROM request_usage
			WHERE created_at_unix >= ?
			ORDER BY id DESC
			LIMIT ?
		)
		SELECT
			COALESCE(status, 0),
			COALESCE(created_at, ''),
			COALESCE(error, ''),
			COALESCE(endpoint, ''),
			`+effectiveModelExpr+`
		FROM sampled
		WHERE status >= 400
		ORDER BY id DESC LIMIT 1
	`, since.Unix(), activitySampleLimit).Scan(
		&row.LatestErrorStatus, &row.LatestErrorAt, &row.LatestError,
		&row.LatestErrorEndpoint, &row.LatestErrorModel,
	)
	if err == sql.ErrNoRows {
		return row, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (db *DB) percentileInt(since time.Time, column string, percentile float64, limit int) (int64, error) {
	switch column {
	case "latency_ms", "ttfb_ms":
	default:
		return 0, fmt.Errorf("unsupported percentile column %q", column)
	}
	if limit <= 0 {
		limit = activitySampleLimit
	}
	var count int64
	if err := db.read.QueryRow(`
		WITH sampled AS (
			SELECT `+column+`
			FROM request_usage
			WHERE created_at_unix >= ?
			ORDER BY id DESC
			LIMIT ?
		)
		SELECT COUNT(*)
		FROM sampled
		WHERE `+column+` > 0
	`, since.Unix(), limit).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	offset := int64(math.Ceil(percentile*float64(count))) - 1
	if offset < 0 {
		offset = 0
	}
	if offset >= count {
		offset = count - 1
	}
	var value int64
	err := db.read.QueryRow(`
		WITH sampled AS (
			SELECT `+column+`
			FROM request_usage
			WHERE created_at_unix >= ?
			ORDER BY id DESC
			LIMIT ?
		)
		SELECT `+column+`
		FROM sampled
		WHERE `+column+` > 0
		ORDER BY `+column+` ASC
		LIMIT 1 OFFSET ?
	`, since.Unix(), limit, offset).Scan(&value)
	return value, err
}

func (db *DB) Requests(limit int, statusMin, statusMax int, model, endpoint, errorClass string, since time.Time) ([]RequestRow, error) {
	query := "SELECT id, COALESCE(created_at, ''), COALESCE(request_id, ''), COALESCE(endpoint, ''), COALESCE(method, ''), COALESCE(status, 0), COALESCE(latency_ms, 0), COALESCE(ttfb_ms, 0), COALESCE(stream, 0), COALESCE(client_ip_hash, ''), COALESCE(api_key_hash, ''), COALESCE(model_requested, ''), COALESCE(model_returned, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(reasoning_tokens, 0), COALESCE(cached_tokens, 0), COALESCE(cache_creation_tokens, 0), COALESCE(total_tokens, 0), COALESCE(request_bytes, 0), COALESCE(response_bytes, 0), COALESCE(error, ''), COALESCE(endpoint_profile, ''), COALESCE(capture_mode, ''), COALESCE(metering_kind, ''), COALESCE(capture_outcome, ''), COALESCE(capture_reason, ''), COALESCE(error_class, ''), COALESCE(error_type, ''), COALESCE(error_code, ''), COALESCE(error_param, ''), COALESCE(error_message, ''), COALESCE(error_message_truncated, 0), COALESCE(model_returned_source, ''), COALESCE(usage_source, ''), COALESCE(terminal_event, ''), COALESCE(terminal_reason, ''), COALESCE(side_usage_event_id, 0) FROM request_usage WHERE 1=1"
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
		query += " AND " + effectiveModelExpr + " = ?"
		args = append(args, model)
	}
	if endpoint != "" {
		if profileName, ok := strings.CutPrefix(endpoint, "profile:"); ok {
			query += " AND endpoint_profile = ?"
			args = append(args, profileName)
		} else {
			query += " AND endpoint = ?"
			args = append(args, endpoint)
		}
	}
	if errorClass != "" {
		query += " AND error_class = ?"
		args = append(args, errorClass)
	}

	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RequestRow
	for rows.Next() {
		var r RequestRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.RequestID, &r.Endpoint, &r.Method, &r.Status, &r.LatencyMs, &r.TTFBMs, &r.Stream, &r.ClientIPHash, &r.APIKeyHash, &r.ModelRequested, &r.ModelReturned, &r.InputTokens, &r.OutputTokens, &r.ReasoningTokens, &r.CachedTokens, &r.CacheCreationTokens, &r.TotalTokens, &r.RequestBytes, &r.ResponseBytes, &r.Error, &r.EndpointProfile, &r.CaptureMode, &r.MeteringKind, &r.CaptureOutcome, &r.CaptureReason, &r.ErrorClass, &r.ErrorType, &r.ErrorCode, &r.ErrorParam, &r.ErrorMessage, &r.ErrorMessageTruncated, &r.ModelReturnedSource, &r.UsageSource, &r.TerminalEvent, &r.TerminalReason, &r.SideUsageEventID); err != nil {
			return nil, err
		}
		if r.ModelReturnedSource == "" && strings.TrimSpace(r.ModelReturned) != "" {
			r.ModelReturnedSource = "legacy"
		}
		if r.UsageSource == "" && hasUsageTokens(r.InputTokens, r.OutputTokens, r.ReasoningTokens, r.CachedTokens, r.TotalTokens) {
			r.UsageSource = "http_response"
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) ErrorTimeline(since time.Time) ([]ErrorTimelineRow, error) {
	rows, err := db.read.Query(`
		SELECT
			timestamp,
			timestamp_unix,
			parse_error_total,
			db_write_error_total,
			dropped_events_total
		FROM health_metrics
		WHERE timestamp_unix >= ?
			OR id = (
				SELECT id FROM health_metrics
				WHERE timestamp_unix < ?
				ORDER BY timestamp_unix DESC, id DESC LIMIT 1
			)
		ORDER BY timestamp_unix ASC, id ASC
	`, since.Unix(), since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ErrorTimelineRow
	var prevParse, prevDB, prevDropped int64
	havePrev := false
	for rows.Next() {
		var timestamp string
		var timestampUnix int64
		var parseErrors, dbErrors, droppedEvents int64
		if err := rows.Scan(&timestamp, &timestampUnix, &parseErrors, &dbErrors, &droppedEvents); err != nil {
			return nil, err
		}
		if !havePrev {
			havePrev = true
			if timestampUnix < since.Unix() {
				prevParse = parseErrors
				prevDB = dbErrors
				prevDropped = droppedEvents
				continue
			}
			// No baseline row exists before the query range. Seed prev from
			// the first in-range row so the delta is zero rather than the raw
			// cumulative value.
			prevParse = parseErrors
			prevDB = dbErrors
			prevDropped = droppedEvents
		}
		r := ErrorTimelineRow{
			Timestamp:     timestamp,
			Count:         0,
			ParseErrors:   positiveDelta(parseErrors, prevParse),
			DBErrors:      positiveDelta(dbErrors, prevDB),
			DroppedEvents: positiveDelta(droppedEvents, prevDropped),
		}
		prevParse = parseErrors
		prevDB = dbErrors
		prevDropped = droppedEvents
		result = append(result, r)
	}
	return result, rows.Err()
}

func positiveDelta(current, previous int64) int64 {
	if current >= previous {
		return current - previous
	}
	return current
}

func (db *DB) LatestHealth() (*HealthRow, error) {
	row := &HealthRow{}
	err := db.read.QueryRow(`
		SELECT COALESCE(timestamp, ''),
			COALESCE(queue_depth, 0),
			COALESCE(dropped_events_total, 0),
			COALESCE(parse_error_total, 0),
			COALESCE(db_write_error_total, 0),
			COALESCE(sse_line_skips_total, 0)
		FROM health_metrics ORDER BY id DESC LIMIT 1
	`).Scan(&row.Timestamp, &row.QueueDepth, &row.DroppedEvents, &row.ParseErrors, &row.DBErrors, &row.SSELineSkips)
	if err == sql.ErrNoRows {
		return row, nil
	}
	return row, err
}

func (db *DB) ErrorTimelineFromRequests(since time.Time) ([]ErrorTimelineRow, error) {
	rows, err := db.read.Query(`
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

func (db *DB) InsertSideUsageEvents(events []SideUsageEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO side_usage_events (received_at, received_at_unix, request_id, matched_request_usage_id, match_status,
			provider, model, alias, endpoint, auth_type, auth_index_hash, source_hash, api_key_hash,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			latency_ms, failed, error_class)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range events {
		receivedAt := e.ReceivedAt
		if receivedAt == "" {
			receivedAt = time.Now().UTC().Format(time.RFC3339)
		}
		receivedAtUnix := e.ReceivedAtUnix
		if receivedAtUnix <= 0 {
			receivedAtUnix, _ = unixFromTimestamp(receivedAt)
		}
		if _, err := stmt.Exec(receivedAt, receivedAtUnix, e.RequestID, e.MatchedRequestUsageID, e.MatchStatus,
			e.Provider, e.Model, e.Alias, e.Endpoint, e.AuthType, e.AuthIndexHash, e.SourceHash, e.APIKeyHash,
			e.InputTokens, e.OutputTokens, e.ReasoningTokens, e.CachedTokens, e.TotalTokens,
			e.LatencyMs, e.Failed, e.ErrorClass); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) InsertSideUsageEvent(e SideUsageEvent) (int64, error) {
	receivedAt := e.ReceivedAt
	if receivedAt == "" {
		receivedAt = time.Now().UTC().Format(time.RFC3339)
	}
	receivedAtUnix := e.ReceivedAtUnix
	if receivedAtUnix <= 0 {
		receivedAtUnix, _ = unixFromTimestamp(receivedAt)
	}
	res, err := db.sql.Exec(`
		INSERT INTO side_usage_events (received_at, received_at_unix, request_id, matched_request_usage_id, match_status,
			provider, model, alias, endpoint, auth_type, auth_index_hash, source_hash, api_key_hash,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			latency_ms, failed, error_class)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, receivedAt, receivedAtUnix, e.RequestID, e.MatchedRequestUsageID, e.MatchStatus,
		e.Provider, e.Model, e.Alias, e.Endpoint, e.AuthType, e.AuthIndexHash, e.SourceHash, e.APIKeyHash,
		e.InputTokens, e.OutputTokens, e.ReasoningTokens, e.CachedTokens, e.TotalTokens,
		e.LatencyMs, e.Failed, e.ErrorClass)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) UpsertCredentialHealth(row *CredentialHealthRow) error {
	_, err := db.sql.Exec(`
		INSERT INTO credential_health (provider, credential_hash, auth_index_hash, label_hash, status, success_count, failed_count, checked_at, checked_at_unix, error_class)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, credential_hash) DO UPDATE SET
			auth_index_hash = excluded.auth_index_hash,
			label_hash = excluded.label_hash,
			status = excluded.status,
			success_count = excluded.success_count,
			failed_count = excluded.failed_count,
			checked_at = excluded.checked_at,
			checked_at_unix = excluded.checked_at_unix,
			error_class = excluded.error_class
	`, row.Provider, row.CredentialHash, row.AuthIndexHash, row.LabelHash, row.Status,
		row.SuccessCount, row.FailedCount, row.CheckedAt, row.CheckedAtUnix, row.ErrorClass)
	return err
}

func (db *DB) AllCredentialHealth() ([]CredentialHealthRow, error) {
	rows, err := db.read.Query(`SELECT provider, credential_hash, auth_index_hash, label_hash, status, success_count, failed_count, checked_at, checked_at_unix, error_class FROM credential_health ORDER BY provider, credential_hash`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CredentialHealthRow
	for rows.Next() {
		var r CredentialHealthRow
		if err := rows.Scan(&r.Provider, &r.CredentialHash, &r.AuthIndexHash, &r.LabelHash, &r.Status, &r.SuccessCount, &r.FailedCount, &r.CheckedAt, &r.CheckedAtUnix, &r.ErrorClass); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) UpsertQuotaCurrent(row *QuotaCurrentRow) error {
	_, err := db.sql.Exec(`
		INSERT INTO quota_current (provider, credential_hash, window_key, expires_at, expires_at_unix,
			checked_at, checked_at_unix, plan, limit_amount, remaining_amount, used_amount, unit,
			reset_at, reset_at_unix, status, quota_supported, adapter_status, error_class, error_message, partial)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, credential_hash, window_key) DO UPDATE SET
			expires_at = excluded.expires_at,
			expires_at_unix = excluded.expires_at_unix,
			checked_at = excluded.checked_at,
			checked_at_unix = excluded.checked_at_unix,
			plan = excluded.plan,
			limit_amount = excluded.limit_amount,
			remaining_amount = excluded.remaining_amount,
			used_amount = excluded.used_amount,
			unit = excluded.unit,
			reset_at = excluded.reset_at,
			reset_at_unix = excluded.reset_at_unix,
			status = excluded.status,
			quota_supported = excluded.quota_supported,
			adapter_status = excluded.adapter_status,
			error_class = excluded.error_class,
			error_message = excluded.error_message,
			partial = excluded.partial
	`, row.Provider, row.CredentialHash, row.WindowKey, row.ExpiresAt, row.ExpiresAtUnix,
		row.CheckedAt, row.CheckedAtUnix, row.Plan, row.LimitAmount, row.RemainingAmount, row.UsedAmount, row.Unit,
		row.ResetAt, row.ResetAtUnix, row.Status, row.QuotaSupported, row.AdapterStatus, row.ErrorClass, row.ErrorMessage, row.Partial)
	return err
}

func (db *DB) AllQuotaCurrent() ([]QuotaCurrentRow, error) {
	rows, err := db.read.Query(`SELECT provider, credential_hash, window_key, expires_at, expires_at_unix, checked_at, checked_at_unix, plan, limit_amount, remaining_amount, used_amount, unit, reset_at, reset_at_unix, status, quota_supported, adapter_status, error_class, error_message, partial FROM quota_current ORDER BY provider, credential_hash`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []QuotaCurrentRow
	for rows.Next() {
		var r QuotaCurrentRow
		if err := rows.Scan(&r.Provider, &r.CredentialHash, &r.WindowKey, &r.ExpiresAt, &r.ExpiresAtUnix,
			&r.CheckedAt, &r.CheckedAtUnix, &r.Plan, &r.LimitAmount, &r.RemainingAmount, &r.UsedAmount, &r.Unit,
			&r.ResetAt, &r.ResetAtUnix, &r.Status, &r.QuotaSupported, &r.AdapterStatus, &r.ErrorClass, &r.ErrorMessage, &r.Partial); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (db *DB) InsertQuotaRefreshEvent(row *QuotaRefreshEventRow) error {
	_, err := db.sql.Exec(`
		INSERT INTO quota_refresh_events (checked_at, checked_at_unix, provider, credential_hash, phase, status, adapter_status, duration_ms, error_class, error_message, partial)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, row.CheckedAt, row.CheckedAtUnix, row.Provider, row.CredentialHash, row.Phase, row.Status, row.AdapterStatus, row.DurationMs, row.ErrorClass, row.ErrorMessage, row.Partial)
	return err
}

func (db *DB) DeleteStaleSideUsageEvents(cutoff time.Time) error {
	_, err := db.sql.Exec(`DELETE FROM side_usage_events WHERE received_at_unix < ?`, cutoff.Unix())
	return err
}

func (db *DB) DeleteStaleQuotaRefreshEvents(cutoff time.Time) error {
	_, err := db.sql.Exec(`DELETE FROM quota_refresh_events WHERE checked_at_unix < ?`, cutoff.Unix())
	return err
}

func (db *DB) DeleteStaleCredentialHealth(cutoff time.Time) error {
	_, err := db.sql.Exec(`DELETE FROM credential_health WHERE checked_at_unix > 0 AND checked_at_unix < ? AND status = 'stale'`, cutoff.Unix())
	return err
}

func (db *DB) FindSideUsageEventByRequestID(requestID string) (*SideUsageEvent, error) {
	if requestID == "" {
		return nil, nil
	}
	row := db.read.QueryRow(`
		SELECT id, received_at, received_at_unix, request_id, matched_request_usage_id, match_status,
			provider, model, alias, endpoint, auth_type, auth_index_hash, source_hash, api_key_hash,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			latency_ms, failed, error_class
		FROM side_usage_events WHERE request_id = ? ORDER BY id ASC LIMIT 1
	`, requestID)
	var e SideUsageEvent
	err := row.Scan(&e.ID, &e.ReceivedAt, &e.ReceivedAtUnix, &e.RequestID, &e.MatchedRequestUsageID, &e.MatchStatus,
		&e.Provider, &e.Model, &e.Alias, &e.Endpoint, &e.AuthType, &e.AuthIndexHash, &e.SourceHash, &e.APIKeyHash,
		&e.InputTokens, &e.OutputTokens, &e.ReasoningTokens, &e.CachedTokens, &e.TotalTokens,
		&e.LatencyMs, &e.Failed, &e.ErrorClass)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (db *DB) UpdateSideUsageEventMatchStatus(id int64, matchStatus string, matchedRequestUsageID int64) error {
	_, err := db.sql.Exec(`UPDATE side_usage_events SET match_status = ?, matched_request_usage_id = ? WHERE id = ?`, matchStatus, matchedRequestUsageID, id)
	return err
}

func (db *DB) UpdateRequestUsageFromSideEvent(id int64, usageSource string, sideUsageEventID int64) error {
	_, err := db.sql.Exec(`UPDATE request_usage SET usage_source = ?, side_usage_event_id = ? WHERE id = ?`, usageSource, sideUsageEventID, id)
	return err
}

func (db *DB) ApplySideUsageEvent(id int64, matchTimeout time.Duration) (string, error) {
	if id <= 0 {
		return "unmatched", nil
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var e SideUsageEvent
	err = tx.QueryRow(`
		SELECT id, received_at, received_at_unix, request_id, matched_request_usage_id, match_status,
			provider, model, alias, endpoint, auth_type, auth_index_hash, source_hash, api_key_hash,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			latency_ms, failed, error_class
		FROM side_usage_events WHERE id = ?
	`, id).Scan(&e.ID, &e.ReceivedAt, &e.ReceivedAtUnix, &e.RequestID, &e.MatchedRequestUsageID, &e.MatchStatus,
		&e.Provider, &e.Model, &e.Alias, &e.Endpoint, &e.AuthType, &e.AuthIndexHash, &e.SourceHash, &e.APIKeyHash,
		&e.InputTokens, &e.OutputTokens, &e.ReasoningTokens, &e.CachedTokens, &e.TotalTokens,
		&e.LatencyMs, &e.Failed, &e.ErrorClass)
	if err == sql.ErrNoRows {
		return "unmatched", nil
	}
	if err != nil {
		return "", err
	}

	updateStatus := func(status string, requestUsageID int64) (string, error) {
		if _, err := tx.Exec(`UPDATE side_usage_events SET match_status = ?, matched_request_usage_id = ? WHERE id = ?`, status, requestUsageID, id); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return status, nil
	}
	if e.RequestID == "" {
		return updateStatus("unmatched", 0)
	}

	var existingMatched int64
	if err := tx.QueryRow(`SELECT COALESCE(id, 0) FROM side_usage_events WHERE request_id = ? AND id <> ? AND match_status = 'matched' ORDER BY id ASC LIMIT 1`, e.RequestID, id).Scan(&existingMatched); err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if existingMatched > 0 {
		return updateStatus("duplicate", 0)
	}

	timeoutSeconds := int64(matchTimeout.Seconds())
	if timeoutSeconds <= 0 {
		timeoutSeconds = 600
	}
	low := e.ReceivedAtUnix - timeoutSeconds
	high := e.ReceivedAtUnix + timeoutSeconds

	var req struct {
		ID                  int64
		InputTokens         int64
		OutputTokens        int64
		ReasoningTokens     int64
		CachedTokens        int64
		TotalTokens         int64
		ModelReturned       string
		ModelReturnedSource string
		UsageSource         string
		SideUsageEventID    int64
	}
	err = tx.QueryRow(`
		SELECT id, COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(reasoning_tokens, 0),
			COALESCE(cached_tokens, 0), COALESCE(total_tokens, 0), COALESCE(model_returned, ''),
			COALESCE(model_returned_source, ''), COALESCE(usage_source, ''), COALESCE(side_usage_event_id, 0)
		FROM request_usage
		WHERE request_id = ? AND created_at_unix BETWEEN ? AND ?
		ORDER BY id ASC LIMIT 1
	`, e.RequestID, low, high).Scan(&req.ID, &req.InputTokens, &req.OutputTokens, &req.ReasoningTokens,
		&req.CachedTokens, &req.TotalTokens, &req.ModelReturned, &req.ModelReturnedSource, &req.UsageSource, &req.SideUsageEventID)
	if err == sql.ErrNoRows {
		var anyRequest int64
		if countErr := tx.QueryRow(`SELECT COALESCE(id, 0) FROM request_usage WHERE request_id = ? ORDER BY id ASC LIMIT 1`, e.RequestID).Scan(&anyRequest); countErr != nil && countErr != sql.ErrNoRows {
			return "", countErr
		}
		if anyRequest > 0 {
			return updateStatus("expired", 0)
		}
		return updateStatus("unmatched", 0)
	}
	if err != nil {
		return "", err
	}
	if req.SideUsageEventID > 0 && req.SideUsageEventID != id {
		return updateStatus("duplicate", req.ID)
	}

	requestHasTokens := hasUsageTokens(req.InputTokens, req.OutputTokens, req.ReasoningTokens, req.CachedTokens, req.TotalTokens)
	sideHasTokens := hasUsageTokens(e.InputTokens, e.OutputTokens, e.ReasoningTokens, e.CachedTokens, e.TotalTokens)
	if requestHasTokens && sideHasTokens && !sameUsageTokens(req.InputTokens, req.OutputTokens, req.ReasoningTokens, req.CachedTokens, req.TotalTokens, e) {
		return updateStatus("conflict", req.ID)
	}

	if !requestHasTokens && sideHasTokens {
		if _, err := tx.Exec(`
			UPDATE request_usage SET
				input_tokens = ?, output_tokens = ?, reasoning_tokens = ?, cached_tokens = ?, total_tokens = ?,
				model_returned = CASE WHEN NULLIF(TRIM(model_returned), '') IS NULL AND ? <> '' THEN ? ELSE model_returned END,
				model_returned_source = CASE WHEN NULLIF(TRIM(model_returned), '') IS NULL AND ? <> '' THEN 'side_channel' ELSE model_returned_source END,
				usage_source = 'cliproxy_side_channel',
				side_usage_event_id = ?
			WHERE id = ?
		`, e.InputTokens, e.OutputTokens, e.ReasoningTokens, e.CachedTokens, e.TotalTokens,
			e.Model, e.Model, e.Model, id, req.ID); err != nil {
			return "", err
		}
	} else if _, err := tx.Exec(`UPDATE request_usage SET side_usage_event_id = ? WHERE id = ?`, id, req.ID); err != nil {
		return "", err
	}
	return updateStatus("matched", req.ID)
}

func hasUsageTokens(input, output, reasoning, cached, total int64) bool {
	return input > 0 || output > 0 || reasoning > 0 || cached > 0 || total > 0
}

func sameUsageTokens(input, output, reasoning, cached, total int64, e SideUsageEvent) bool {
	return input == e.InputTokens &&
		output == e.OutputTokens &&
		reasoning == e.ReasoningTokens &&
		cached == e.CachedTokens &&
		total == e.TotalTokens
}
