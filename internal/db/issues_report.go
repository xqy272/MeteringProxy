package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// IssueFilter is the typed exact-scope filter for the issues report.
// When IncludeGlobal is false, only request_usage issues are queried.
type IssueFilter struct {
	Scope         ReportScope
	Limit         int
	IncludeGlobal bool
}

// IssuesReportData carries per-source issue rows and query/scan errors.
// Report assembly decides atomic failure vs partial; DB never swallows errors.
type IssuesReportData struct {
	RequestUsage   []IssueRow
	SideChannel    []IssueRow
	SideChannelErr error
	Credential     []IssueRow
	CredentialErr  error
	Quota          []IssueRow
	QuotaErr       error
}

// IssuesReport loads multi-source issue rows with context and typed scope/filter.
// request_usage always runs. Optional global sources run only when IncludeGlobal.
// Context cancellation/deadline from any source returns immediately as a top-level
// error and stops further source queries. Non-context optional source failures stay
// in the per-source error fields for partial assembly.
func (db *DB) IssuesReport(ctx context.Context, filter IssueFilter) (*IssuesReportData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	out := &IssuesReportData{}
	rows, err := queryRequestUsageIssues(ctx, db, filter.Scope, limit)
	if err != nil {
		// Core request_usage failure is always top-level (including cancellation).
		return nil, err
	}
	out.RequestUsage = rows

	if !filter.IncludeGlobal {
		return out, nil
	}

	sideRows, sideErr := querySideChannelIssues(ctx, db, filter.Scope.Since)
	if isContextError(sideErr) {
		return nil, sideErr
	}
	out.SideChannel = sideRows
	out.SideChannelErr = sideErr

	credRows, credErr := queryCredentialIssues(ctx, db)
	if isContextError(credErr) {
		return nil, credErr
	}
	out.Credential = credRows
	out.CredentialErr = credErr

	quotaRows, quotaErr := queryQuotaIssues(ctx, db, filter.Scope.Since)
	if isContextError(quotaErr) {
		return nil, quotaErr
	}
	out.Quota = quotaRows
	out.QuotaErr = quotaErr

	return out, nil
}

func isContextError(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

// ErrorTimelineReport is the context-aware form of ErrorTimeline.
func (db *DB) ErrorTimelineReport(ctx context.Context, since time.Time) ([]ErrorTimelineRow, error) {
	rows, err := db.read.QueryContext(ctx, `
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
	baselineMissing := false
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
			prevParse = parseErrors
			prevDB = dbErrors
			prevDropped = droppedEvents
			baselineMissing = true
		}
		r := ErrorTimelineRow{
			Timestamp:       timestamp,
			Count:           0,
			ParseErrors:     positiveDelta(parseErrors, prevParse),
			DBErrors:        positiveDelta(dbErrors, prevDB),
			DroppedEvents:   positiveDelta(droppedEvents, prevDropped),
			BaselineMissing: baselineMissing,
		}
		baselineMissing = false
		prevParse = parseErrors
		prevDB = dbErrors
		prevDropped = droppedEvents
		result = append(result, r)
	}
	return result, rows.Err()
}

func queryRequestUsageIssues(ctx context.Context, db *DB, scope ReportScope, limit int) ([]IssueRow, error) {
	scopeWhere, scopeArgs := reportScopeWhere(scope)
	args := append(append([]any{}, scopeArgs...), limit)
	rows, err := db.read.QueryContext(ctx,
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
			WHERE `+scopeWhere+` AND (
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
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]IssueRow, 0)
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
	return result, nil
}

func querySideChannelIssues(ctx context.Context, db *DB, since time.Time) ([]IssueRow, error) {
	rows, err := db.read.QueryContext(ctx, `
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
		return nil, err
	}
	defer rows.Close()

	result := make([]IssueRow, 0)
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Endpoint, &r.Model, &r.RequestID, &r.Message); err != nil {
			return nil, err
		}
		r.Label = classLabel(r.Class)
		r.Severity = classSeverity(r.Class)
		r.SourceGroup = "side_channel"
		r.ModelSource = "side_channel"
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func queryCredentialIssues(ctx context.Context, db *DB) ([]IssueRow, error) {
	rows, err := db.read.QueryContext(ctx, `
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
		return nil, err
	}
	defer rows.Close()

	result := make([]IssueRow, 0)
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Endpoint, &r.Message); err != nil {
			return nil, err
		}
		r.Label = classLabel(r.Class)
		r.Severity = classSeverity(r.Class)
		r.SourceGroup = "credential_health"
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func queryQuotaIssues(ctx context.Context, db *DB, since time.Time) ([]IssueRow, error) {
	result := make([]IssueRow, 0)
	var firstErr error

	currentRows, err := queryQuotaCurrentIssues(ctx, db)
	if isContextError(err) {
		return currentRows, err
	}
	if err != nil {
		firstErr = err
	} else {
		result = append(result, currentRows...)
	}

	refreshRows, err := queryQuotaRefreshIssues(ctx, db, since)
	if isContextError(err) {
		return append(result, refreshRows...), err
	}
	if err != nil {
		if firstErr == nil {
			firstErr = err
		} else {
			firstErr = fmt.Errorf("%w; %v", firstErr, err)
		}
	} else {
		result = append(result, refreshRows...)
	}

	return result, firstErr
}

func queryQuotaCurrentIssues(ctx context.Context, db *DB) ([]IssueRow, error) {
	rows, err := db.read.QueryContext(ctx, `
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
		return nil, err
	}
	defer rows.Close()

	result := make([]IssueRow, 0)
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Endpoint, &r.Message); err != nil {
			return nil, err
		}
		r.Label = classLabel(r.Class)
		r.Severity = classSeverity(r.Class)
		r.SourceGroup = "quota"
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func queryQuotaRefreshIssues(ctx context.Context, db *DB, since time.Time) ([]IssueRow, error) {
	rows, err := db.read.QueryContext(ctx, `
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
		return nil, err
	}
	defer rows.Close()

	result := make([]IssueRow, 0)
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Endpoint, &r.Message); err != nil {
			return nil, err
		}
		r.Label = classLabel(r.Class)
		r.Severity = classSeverity(r.Class)
		r.SourceGroup = "quota"
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
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
