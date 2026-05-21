package db

import (
	"fmt"
	"sort"
	"time"
)

// Overview returns a composite overview with selected range, recent 1h,
// capture health, and cost sections.
func (db *DB) Overview(since time.Time) *OverviewRow {
	row := &OverviewRow{Range: "24h"}

	var totalRequests, failedRequests, inputTokens, outputTokens, reasoningTokens, cachedTokens, totalTokens int64
	err := db.read.QueryRow(`
		SELECT COUNT(*), COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM request_usage WHERE created_at_unix >= ?
	`, since.Unix()).Scan(
		&totalRequests, &failedRequests,
		&inputTokens, &outputTokens, &reasoningTokens, &cachedTokens, &totalTokens,
	)
	if err == nil {
		p95Lat, _ := db.percentileInt(since, "latency_ms", 0.95, activitySampleLimit)
		p95TTFB, _ := db.percentileInt(since, "ttfb_ms", 0.95, activitySampleLimit)
		row.Selected.Data = map[string]interface{}{
			"total_requests":         totalRequests,
			"failed_requests":        failedRequests,
			"total_input_tokens":     inputTokens,
			"total_output_tokens":    outputTokens,
			"total_reasoning_tokens": reasoningTokens,
			"total_cached_tokens":    cachedTokens,
			"total_tokens":           totalTokens,
			"p95_latency_ms":         p95Lat,
			"p95_ttfb_ms":            p95TTFB,
		}
	} else {
		row.Selected.Error = err.Error()
	}

	// Recent 1h
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	var rTotal, rFailed int64
	err = db.read.QueryRow(`
		SELECT COUNT(*), COUNT(CASE WHEN status >= 400 THEN 1 END)
		FROM request_usage WHERE created_at_unix >= ?
	`, oneHourAgo.Unix()).Scan(&rTotal, &rFailed)
	if err == nil {
		p95Lat1h, _ := db.percentileInt(oneHourAgo, "latency_ms", 0.95, activitySampleLimit)
		failureRate := 0.0
		if rTotal > 0 {
			failureRate = float64(rFailed) / float64(rTotal)
		}
		recentData := map[string]interface{}{
			"total_requests":  rTotal,
			"failed_requests": rFailed,
			"failure_rate":    failureRate,
			"p95_latency_ms":  p95Lat1h,
			"latest_error":    nil,
		}
		var latest struct {
			CreatedAt   string
			Status      int
			Endpoint    string
			Model       string
			ModelSource string
			Class       string
			Message     string
			RequestID   string
		}
		latestErr := db.read.QueryRow(`
			SELECT
				COALESCE(created_at, ''),
				COALESCE(status, 0),
				COALESCE(endpoint, ''),
				`+effectiveModelExpr+`,
				`+modelSourceExpr+`,
				COALESCE(NULLIF(TRIM(error_class), ''), 'unknown'),
				COALESCE(NULLIF(TRIM(error_code), ''), NULLIF(TRIM(error_type), ''), NULLIF(TRIM(error_class), ''), error, ''),
				COALESCE(request_id, '')
			FROM request_usage
			WHERE created_at_unix >= ? AND status >= 400
			ORDER BY created_at_unix DESC, id DESC
			LIMIT 1
		`, oneHourAgo.Unix()).Scan(
			&latest.CreatedAt, &latest.Status, &latest.Endpoint, &latest.Model,
			&latest.ModelSource, &latest.Class, &latest.Message, &latest.RequestID,
		)
		if latestErr == nil {
			recentData["latest_error"] = map[string]interface{}{
				"latest_at":    latest.CreatedAt,
				"status":       latest.Status,
				"endpoint":     latest.Endpoint,
				"model":        latest.Model,
				"model_source": latest.ModelSource,
				"class":        latest.Class,
				"message":      latest.Message,
				"request_id":   latest.RequestID,
			}
		}
		row.Recent1h.Data = recentData
	} else {
		row.Recent1h.Error = err.Error()
	}

	row.Capture.Data = map[string]interface{}{"status": "healthy"}
	row.Cost.Data = map[string]interface{}{"partial": false}
	return row
}

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
			CASE status
				WHEN 'error' THEN 'credential_error'
				WHEN 'stale' THEN 'credential_stale'
				WHEN 'disabled' THEN 'credential_disabled'
				ELSE 'credential_unavailable'
			END AS class,
			COUNT(*) AS count,
			MAX(COALESCE(checked_at, '')) AS latest_at,
			COALESCE(provider, '') AS endpoint,
			COALESCE(error_class, '') AS message
		FROM credential_health
		WHERE status IN ('unavailable', 'disabled', 'error', 'stale')
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
	case "invalid_request":
		return "Invalid request"
	case "context_length":
		return "Context length exceeded"
	case "upstream_5xx":
		return "Upstream 5xx error"
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
	case "auth_failed", "quota_exhausted", "proxy_upstream_error", "proxy_connection_refused", "proxy_connection_reset",
		"proxy_timeout", "proxy_dns_error", "proxy_network_unreachable", "proxy_tls_error", "db_write_error",
		"response_error_event", "credential_error", "credential_disabled", "usage_conflict":
		return "error"
	case "rate_limited", "upstream_5xx", "context_length", "capture_parse_error", "dropped_event",
		"response_completed_without_usage", "stream_ended_without_completed", "response_incomplete",
		"proxy_connection_closed", "credential_unavailable", "credential_stale", "quota_low", "quota_refresh_failed", "quota_stale", "quota_unsupported", "quota_unknown",
		"side_channel_expired", "side_channel_invalid_payload", "side_channel_unmatched":
		return "warning"
	default:
		return "info"
	}
}

func (db *DB) OverviewCaptureStats(since time.Time) (failed, skipped int64, err error) {
	if err := db.read.QueryRow(`
		SELECT COUNT(*) FROM request_usage
		WHERE created_at_unix >= ? AND capture_outcome = 'failed'
	`, since.Unix()).Scan(&failed); err != nil {
		return 0, 0, fmt.Errorf("capture failed stats: %w", err)
	}
	if err := db.read.QueryRow(`
		SELECT COUNT(*) FROM request_usage
		WHERE created_at_unix >= ? AND capture_outcome = 'skipped'
	`, since.Unix()).Scan(&skipped); err != nil {
		return 0, 0, fmt.Errorf("capture skipped stats: %w", err)
	}
	return failed, skipped, nil
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
