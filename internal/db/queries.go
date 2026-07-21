package db

import (
	"database/sql"
	"fmt"
	"log"
	"sort"
	"time"
)

// Issues returns aggregated request-level issues.
func (db *DB) Issues(since time.Time, limit int) ([]IssueRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.read.Query(
		`WITH base AS (
			SELECT
				id,
				COALESCE(created_at, '') AS created_at,
				COALESCE(created_at_unix, 0) AS created_at_unix,
				CASE
					WHEN status >= 400 AND NULLIF(TRIM(error_class), '') IS NOT NULL THEN COALESCE(NULLIF(TRIM(error_class), ''), 'unknown')
					WHEN capture_outcome = 'failed' AND NULLIF(TRIM(capture_reason), '') IS NOT NULL THEN capture_reason
					WHEN capture_outcome = 'skipped' AND NULLIF(TRIM(capture_reason), '') IS NOT NULL THEN capture_reason
					WHEN capture_outcome = 'failed' THEN 'capture_failed'
					ELSE 'unknown'
				END AS class,
				COALESCE(status, 0) AS status,
				COALESCE(endpoint, '') AS endpoint,
				`+effectiveModelExpr+` AS model,
				COALESCE(api_key_hash, '') AS api_key_hash,
				CASE WHEN NULLIF(TRIM(model_returned), '') IS NOT NULL THEN 1 ELSE 0 END AS returned_present,
				CASE WHEN NULLIF(TRIM(model_requested), '') IS NOT NULL THEN 1 ELSE 0 END AS requested_present,
				COALESCE(error_type, '') AS error_type,
				COALESCE(error_code, '') AS error_code,
				COALESCE(NULLIF(TRIM(error_code), ''), NULLIF(TRIM(error_type), ''), NULLIF(TRIM(error_class), ''), '') AS error_message,
				COALESCE(request_id, '') AS request_id
			FROM request_usage
			WHERE created_at_unix >= ? AND (
				status >= 400
				OR (capture_outcome = 'failed' AND capture_reason != '')
				OR (capture_outcome = 'skipped' AND capture_reason != '')
			)
		),
		agg AS (
			SELECT
				class,
				status,
				endpoint,
				model,
				api_key_hash,
				COUNT(*) AS count,
				MAX(created_at_unix) AS latest_unix,
				CASE
					WHEN SUM(returned_present) > 0 THEN 'returned'
					WHEN SUM(requested_present) > 0 THEN 'requested'
					ELSE 'unidentified'
				END AS model_source
			FROM base
			GROUP BY class, status, endpoint, model, api_key_hash
		),
		latest AS (
			SELECT *,
				ROW_NUMBER() OVER (
					PARTITION BY class, status, endpoint, model, api_key_hash
					ORDER BY created_at_unix DESC, id DESC
				) AS rn
			FROM base
		)
		SELECT
			agg.class,
			agg.count,
			latest.created_at,
			agg.status,
			agg.endpoint,
			agg.model,
			agg.model_source,
			agg.api_key_hash,
			latest.error_type,
			latest.error_code,
			latest.error_message,
			latest.request_id
		FROM agg
		JOIN latest
			ON latest.rn = 1
			AND latest.class = agg.class
			AND latest.status = agg.status
			AND latest.endpoint = agg.endpoint
			AND latest.model = agg.model
			AND latest.api_key_hash = agg.api_key_hash
		ORDER BY
			CASE
				WHEN agg.class IN ('auth_failed', 'quota_exhausted', 'proxy_upstream_error', 'proxy_connection_refused',
					'proxy_connection_reset', 'proxy_timeout', 'proxy_dns_error', 'proxy_network_unreachable',
					'proxy_tls_error', 'proxy_connection_closed', 'db_write_error', 'response_error_event') THEN 0
				WHEN agg.class IN ('rate_limited', 'upstream_5xx', 'context_length', 'capture_parse_error', 'dropped_event',
					'response_completed_without_usage', 'stream_ended_without_completed', 'response_incomplete',
					'capture_failed', 'parse_error', 'usage_not_present') THEN 1
				ELSE 2
			END ASC,
			agg.latest_unix DESC,
			agg.count DESC
		LIMIT ?`,
		since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []IssueRow
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Status, &r.Endpoint, &r.Model, &r.ModelSource, &r.APIKeyHash, &r.ErrorType, &r.ErrorCode, &r.Message, &r.RequestID); err != nil {
			return nil, err
		}
		r.Label = classLabel(r.Class)
		r.Severity = classSeverity(r.Class)
		r.SourceGroup = "request_usage"
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result = db.appendSideChannelIssues(result, since)
	result = db.appendCredentialIssues(result, since)
	result = db.appendQuotaIssues(result, since)
	sortIssueRows(result)
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (db *DB) appendSideChannelIssues(result []IssueRow, since time.Time) []IssueRow {
	rows, err := db.read.Query(`
		WITH base AS (
			SELECT
				CASE match_status
					WHEN 'conflict' THEN 'usage_conflict'
					WHEN 'duplicate' THEN 'side_channel_duplicate'
					WHEN 'expired' THEN 'side_channel_expired'
					WHEN 'invalid_payload' THEN 'side_channel_invalid_payload'
					ELSE 'side_channel_unmatched'
				END AS class,
				COALESCE(received_at, '') AS latest_at,
				COALESCE(received_at_unix, 0) AS latest_unix,
				COALESCE(endpoint, '') AS endpoint,
				COALESCE(model, '') AS model,
				COALESCE(request_id, '') AS request_id,
				COALESCE(error_class, '') AS message
			FROM side_usage_events
			WHERE received_at_unix >= ? AND match_status IN ('conflict', 'duplicate', 'expired', 'invalid_payload', 'unmatched')
		),
		agg AS (
			SELECT class, endpoint, model, COUNT(*) AS count, MAX(latest_unix) AS latest_unix
			FROM base GROUP BY class, endpoint, model
		),
		latest AS (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY class, endpoint, model ORDER BY latest_unix DESC) AS rn
			FROM base
		)
		SELECT agg.class, agg.count, latest.latest_at, agg.endpoint, agg.model, latest.request_id, latest.message
		FROM agg JOIN latest ON latest.rn = 1 AND latest.class = agg.class AND latest.endpoint = agg.endpoint AND latest.model = agg.model
	`, since.Unix())
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Endpoint, &r.Model, &r.RequestID, &r.Message); err == nil {
			r.Label = classLabel(r.Class)
			r.Severity = classSeverity(r.Class)
			r.SourceGroup = "side_channel"
			r.ModelSource = "side_channel"
			result = append(result, r)
		}
	}
	return result
}

func (db *DB) appendCredentialIssues(result []IssueRow, since time.Time) []IssueRow {
	rows, err := db.read.Query(`
		SELECT
			CASE
				WHEN error_class = 'credential_quota_limited' THEN 'credential_quota_limited'
				WHEN status = 'warning' THEN 'credential_history_warning'
				WHEN status = 'error' THEN 'credential_error'
				WHEN status = 'stale' THEN 'credential_stale'
				WHEN status = 'disabled' THEN 'credential_disabled'
				ELSE 'credential_unavailable'
			END AS class,
			COUNT(*) AS count,
			MAX(COALESCE(checked_at, '')) AS latest_at,
			COALESCE(provider, '') AS endpoint,
			COALESCE(error_class, '') AS message
		FROM credential_health
		WHERE status IN ('unavailable', 'disabled', 'error', 'stale', 'warning')
		GROUP BY class, provider, error_class
	`)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Endpoint, &r.Message); err == nil {
			r.Label = classLabel(r.Class)
			r.Severity = classSeverity(r.Class)
			r.SourceGroup = "credential_health"
			result = append(result, r)
		}
	}
	return result
}

func (db *DB) appendQuotaIssues(result []IssueRow, since time.Time) []IssueRow {
	rows, err := db.read.Query(`
		SELECT
			CASE status
				WHEN 'low' THEN 'quota_low'
				WHEN 'exhausted' THEN 'quota_exhausted'
				WHEN 'stale' THEN 'quota_stale'
				WHEN 'unsupported' THEN 'quota_unsupported'
				WHEN 'unknown' THEN 'quota_unknown'
				ELSE 'quota_refresh_failed'
			END AS class,
			COUNT(*) AS count,
			MAX(COALESCE(checked_at, '')) AS latest_at,
			COALESCE(provider, '') AS endpoint,
			COALESCE(error_class, '') AS message
		FROM quota_current
		WHERE status IN ('low', 'exhausted', 'error', 'stale', 'unsupported', 'unknown')
		GROUP BY class, provider, error_class
	`)
	if err != nil {
		return db.appendQuotaRefreshIssues(result, since)
	}
	defer rows.Close()
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Endpoint, &r.Message); err == nil {
			r.Label = classLabel(r.Class)
			r.Severity = classSeverity(r.Class)
			r.SourceGroup = "quota"
			result = append(result, r)
		}
	}
	return db.appendQuotaRefreshIssues(result, since)
}

func (db *DB) appendQuotaRefreshIssues(result []IssueRow, since time.Time) []IssueRow {
	rows, err := db.read.Query(`
		SELECT
			'quota_refresh_failed' AS class,
			COUNT(*) AS count,
			MAX(COALESCE(checked_at, '')) AS latest_at,
			COALESCE(provider, '') AS endpoint,
			COALESCE(NULLIF(TRIM(error_class), ''), NULLIF(TRIM(adapter_status), ''), 'quota_refresh_failed') AS message
		FROM quota_refresh_events
		WHERE checked_at_unix >= ? AND status = 'error'
		GROUP BY provider, message
	`, since.Unix())
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Endpoint, &r.Message); err == nil {
			r.Label = classLabel(r.Class)
			r.Severity = classSeverity(r.Class)
			r.SourceGroup = "quota"
			result = append(result, r)
		}
	}
	return result
}

func sortIssueRows(rows []IssueRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if severityRank(rows[i].Severity) != severityRank(rows[j].Severity) {
			return severityRank(rows[i].Severity) < severityRank(rows[j].Severity)
		}
		if rows[i].LatestAt != rows[j].LatestAt {
			return rows[i].LatestAt > rows[j].LatestAt
		}
		return rows[i].Count > rows[j].Count
	})
}

func severityRank(severity string) int {
	switch severity {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

func classLabel(class string) string {
	switch class {
	case "quota_exhausted":
		return "Quota exhausted"
	case "rate_limited":
		return "Rate limited"
	case "auth_failed":
		return "Auth failed"
	case "auth_invalid_key":
		return "Invalid API key"
	case "auth_expired":
		return "Auth expired"
	case "billing_required":
		return "Billing required"
	case "permission_denied":
		return "Permission denied"
	case "invalid_request":
		return "Invalid request"
	case "invalid_model":
		return "Invalid model"
	case "context_length":
		return "Context length exceeded"
	case "upstream_5xx":
		return "Upstream 5xx error"
	case "upstream_internal_error":
		return "Upstream internal error"
	case "upstream_not_implemented":
		return "Upstream not implemented"
	case "upstream_bad_gateway":
		return "Upstream bad gateway"
	case "upstream_unavailable":
		return "Upstream unavailable"
	case "upstream_timeout":
		return "Upstream timeout"
	case "upstream_connection_refused":
		return "Upstream connection refused"
	case "upstream_connection_reset":
		return "Upstream connection reset"
	case "upstream_dns_error":
		return "Upstream DNS error"
	case "upstream_network_unreachable":
		return "Upstream network unreachable"
	case "upstream_tls_error":
		return "Upstream TLS error"
	case "upstream_overloaded":
		return "Upstream overloaded"
	case "not_found":
		return "Not found"
	case "request_timeout":
		return "Request timeout"
	case "conflict":
		return "Conflict"
	case "request_too_large":
		return "Request too large"
	case "validation_error":
		return "Validation error"
	case "proxy_upstream_error":
		return "Proxy upstream error"
	case "proxy_connection_refused":
		return "Proxy connection refused"
	case "proxy_connection_reset":
		return "Proxy connection reset"
	case "proxy_timeout":
		return "Proxy upstream timeout"
	case "proxy_dns_error":
		return "Proxy DNS error"
	case "proxy_network_unreachable":
		return "Proxy network unreachable"
	case "proxy_tls_error":
		return "Proxy TLS error"
	case "proxy_connection_closed":
		return "Proxy connection closed"
	case "capture_parse_error":
		return "Capture parse error"
	case "db_write_error":
		return "DB write error"
	case "dropped_event":
		return "Dropped event"
	case "response_completed_without_usage":
		return "Response completed without usage data"
	case "stream_ended_without_completed":
		return "Stream ended without completion"
	case "response_error_event":
		return "Response error event"
	case "response_incomplete":
		return "Response incomplete"
	case "credential_unavailable":
		return "Credential unavailable"
	case "credential_disabled":
		return "Credential disabled"
	case "quota_low":
		return "Quota low"
	case "quota_refresh_failed":
		return "Quota refresh failed"
	case "quota_stale":
		return "Quota data stale"
	case "quota_unsupported":
		return "Quota unsupported"
	case "quota_unknown":
		return "Quota unknown"
	case "credential_error":
		return "Credential health error"
	case "credential_stale":
		return "Credential health stale"
	case "credential_quota_limited":
		return "Credential quota signal"
	case "credential_history_warning":
		return "Credential history warning"
	case "usage_conflict":
		return "Usage conflict"
	case "side_channel_duplicate":
		return "Duplicate side-channel usage"
	case "side_channel_expired":
		return "Expired side-channel usage"
	case "side_channel_invalid_payload":
		return "Invalid side-channel payload"
	case "side_channel_unmatched":
		return "Unmatched side-channel usage"
	default:
		return "Unclassified issue"
	}
}

func classSeverity(class string) string {
	switch class {
	case "auth_failed", "auth_invalid_key", "auth_expired", "billing_required", "permission_denied", "quota_exhausted",
		"proxy_upstream_error", "proxy_connection_refused", "proxy_connection_reset",
		"proxy_timeout", "proxy_dns_error", "proxy_network_unreachable", "proxy_tls_error", "db_write_error",
		"response_error_event", "credential_error", "credential_disabled", "usage_conflict":
		return "error"
	case "rate_limited", "upstream_5xx", "upstream_internal_error", "upstream_not_implemented", "upstream_bad_gateway",
		"upstream_unavailable", "upstream_timeout", "upstream_connection_refused", "upstream_connection_reset",
		"upstream_dns_error", "upstream_network_unreachable", "upstream_tls_error", "upstream_overloaded",
		"context_length", "invalid_request", "invalid_model", "not_found", "conflict", "validation_error",
		"request_timeout", "request_too_large", "capture_parse_error", "dropped_event",
		"response_completed_without_usage", "stream_ended_without_completed", "response_incomplete",
		"proxy_connection_closed", "credential_unavailable", "credential_stale", "quota_low", "quota_refresh_failed", "quota_stale", "quota_unsupported", "quota_unknown",
		"credential_quota_limited", "credential_history_warning", "side_channel_expired", "side_channel_invalid_payload", "side_channel_unmatched":
		return "warning"
	default:
		return "info"
	}
}

func (db *DB) CaptureOutcomeCounts(since time.Time) (captured, skipped, failed int64, err error) {
	if err := db.read.QueryRow(`
		SELECT COUNT(*) FROM request_usage
		WHERE created_at_unix >= ? AND capture_outcome = 'captured'
	`, since.Unix()).Scan(&captured); err != nil {
		return 0, 0, 0, fmt.Errorf("capture outcome count captured: %w", err)
	}
	if err := db.read.QueryRow(`
		SELECT COUNT(*) FROM request_usage
		WHERE created_at_unix >= ? AND capture_outcome = 'skipped'
	`, since.Unix()).Scan(&skipped); err != nil {
		return 0, 0, 0, fmt.Errorf("capture outcome count skipped: %w", err)
	}
	if err := db.read.QueryRow(`
		SELECT COUNT(*) FROM request_usage
		WHERE created_at_unix >= ? AND capture_outcome = 'failed'
	`, since.Unix()).Scan(&failed); err != nil {
		return 0, 0, 0, fmt.Errorf("capture outcome count failed: %w", err)
	}
	return captured, skipped, failed, nil
}

// ModelAssetRow is one per-model aggregate for the model assets view.
type ModelAssetRow struct {
	Model            string `json:"model"`
	RequestCount     int64  `json:"request_count"`
	FailedCount      int64  `json:"failed_count"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	EndpointProfiles string `json:"endpoint_profiles"` // comma-separated distinct values
	CaptureModes     string `json:"capture_modes"`     // comma-separated distinct values
	LatestSeenAt     string `json:"latest_seen_at"`
}

// ModelAssets aggregates request_usage by effective model for the model
// assets view. EndpointProfiles and CaptureModes are comma-separated distinct
// values; the webui layer splits them.
func (db *DB) ModelAssets(since time.Time) ([]ModelAssetRow, error) {
	rows, err := db.read.Query(`
		SELECT
			`+effectiveModelExpr+`,
			COUNT(*),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(GROUP_CONCAT(DISTINCT COALESCE(NULLIF(endpoint_profile, ''), 'unknown')), ''),
			COALESCE(GROUP_CONCAT(DISTINCT COALESCE(NULLIF(capture_mode, ''), 'unknown')), ''),
			MAX(created_at)
		FROM request_usage
		WHERE created_at_unix >= ?
		GROUP BY 1
		ORDER BY COUNT(*) DESC
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("model assets query: %w", err)
	}
	defer rows.Close()

	var result []ModelAssetRow
	for rows.Next() {
		var r ModelAssetRow
		if err := rows.Scan(
			&r.Model, &r.RequestCount, &r.FailedCount,
			&r.InputTokens, &r.OutputTokens, &r.TotalTokens,
			&r.EndpointProfiles, &r.CaptureModes, &r.LatestSeenAt,
		); err != nil {
			return nil, fmt.Errorf("model assets scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// GatewayCapabilityRow is one per-endpoint_profile aggregate for the gateway
// capability view.
type GatewayCapabilityRow struct {
	EndpointProfile   string `json:"endpoint_profile"`
	RequestCount      int64  `json:"request_count"`
	StreamCount       int64  `json:"stream_count"`
	MissingUsageCount int64  `json:"missing_usage_count"`
	UsageMeteredCount int64  `json:"usage_metered_count"`
	RequestOnlyCount  int64  `json:"request_only_count"`
	PassthroughCount  int64  `json:"passthrough_count"`
}

// VerifySaltFingerprint enforces the salt-consistency invariant (CLAUDE.md #7).
// On a fresh DB (no request_usage data, no stored fingerprint) it binds the
// current fingerprint. On a legacy DB (has data, no fingerprint) it performs a
// one-time legacy bind. If a fingerprint is already stored and does not match,
// it returns an error telling the operator how to recover. The salt itself is
// never stored; only the non-reversible fingerprint is persisted.
func (db *DB) VerifySaltFingerprint(fingerprint, dbPath, saltPath string) error {
	var hasData bool
	if err := db.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM request_usage LIMIT 1)`).Scan(&hasData); err != nil {
		return fmt.Errorf("check request_usage data: %w", err)
	}

	var stored string
	err := db.read.QueryRow(`SELECT value FROM db_metadata WHERE key = 'salt_fingerprint'`).Scan(&stored)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read salt fingerprint: %w", err)
	}
	hasFingerprint := err == nil

	if !hasFingerprint {
		// Fresh DB or legacy bind: write the current fingerprint.
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := db.sql.Exec(
			`INSERT INTO db_metadata (key, value, updated_at) VALUES ('salt_fingerprint', ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			fingerprint, now,
		); err != nil {
			return fmt.Errorf("write salt fingerprint: %w", err)
		}
		if hasData {
			log.Printf("salt fingerprint: legacy DB bound to current salt file (one-time migration)")
		}
		return nil
	}

	if stored != fingerprint {
		return fmt.Errorf(
			"salt file has changed but the database already has historical data; "+
				"changing the salt breaks all historical api_key_hash and client_ip_hash groupings.\n"+
				"  database: %s\n"+
				"  salt file: %s\n"+
				"  stored fingerprint: %s\n"+
				"  current fingerprint: %s\n"+
				"recovery: restore the original salt file from backup (it must be backed up alongside the SQLite DB), "+
				"or start with a fresh database if you do not need historical grouping",
			dbPath, saltPath, stored, fingerprint,
		)
	}
	return nil
}

// GatewayCapabilities aggregates request_usage by endpoint_profile for the
// gateway capability view. Rows cover only profiles that had traffic in the
// range; the webui layer merges in zero-traffic profiles from the registry.
func (db *DB) GatewayCapabilities(since time.Time) ([]GatewayCapabilityRow, error) {
	rows, err := db.read.Query(`
		SELECT
			COALESCE(NULLIF(endpoint_profile, ''), 'unknown') AS endpoint_profile,
			COUNT(*) AS request_count,
			SUM(CASE WHEN stream = 1 THEN 1 ELSE 0 END) AS stream_count,
			SUM(CASE WHEN capture_mode = 'usage_metered' AND capture_outcome != 'captured' THEN 1 ELSE 0 END) AS missing_usage_count,
			SUM(CASE WHEN capture_mode = 'usage_metered' THEN 1 ELSE 0 END) AS usage_metered_count,
			SUM(CASE WHEN capture_mode = 'request_only' THEN 1 ELSE 0 END) AS request_only_count,
			SUM(CASE WHEN capture_mode = 'passthrough' THEN 1 ELSE 0 END) AS passthrough_count
		FROM request_usage
		WHERE created_at_unix >= ?
		GROUP BY COALESCE(NULLIF(endpoint_profile, ''), 'unknown')
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("gateway capabilities query: %w", err)
	}
	defer rows.Close()

	var result []GatewayCapabilityRow
	for rows.Next() {
		var r GatewayCapabilityRow
		if err := rows.Scan(
			&r.EndpointProfile,
			&r.RequestCount,
			&r.StreamCount,
			&r.MissingUsageCount,
			&r.UsageMeteredCount,
			&r.RequestOnlyCount,
			&r.PassthroughCount,
		); err != nil {
			return nil, fmt.Errorf("gateway capabilities scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
