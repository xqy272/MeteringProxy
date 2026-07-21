package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// MultimodalSummaryReport is the context-aware multimodal dimension aggregate.
func (db *DB) MultimodalSummaryReport(ctx context.Context, since time.Time) ([]MultimodalSummaryRow, error) {
	rows, err := db.read.QueryContext(ctx, `
		SELECT
			COALESCE(NULLIF(TRIM(modality), ''), 'unknown'),
			COALESCE(NULLIF(TRIM(channel), ''), 'unknown'),
			COALESCE(NULLIF(TRIM(metric), ''), 'unknown'),
			COALESCE(NULLIF(TRIM(direction), ''), 'unknown'),
			COALESCE(NULLIF(TRIM(unit), ''), 'unknown'),
			COALESCE(SUM(amount), 0),
			COUNT(DISTINCT request_usage_id)
		FROM usage_dimensions
		WHERE created_at_unix >= ?
		GROUP BY 1, 2, 3, 4, 5
		ORDER BY 1, 2, 3, 4, 5
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []MultimodalSummaryRow
	for rows.Next() {
		var r MultimodalSummaryRow
		if err := rows.Scan(&r.Modality, &r.Channel, &r.Metric, &r.Direction, &r.Unit, &r.Amount, &r.RequestCount); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ImageRequestsReport is the context-aware image-request list query.
func (db *DB) ImageRequestsReport(ctx context.Context, limit int, since time.Time) ([]RequestRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.read.QueryContext(ctx, `
		SELECT id, COALESCE(created_at, ''), COALESCE(request_id, ''), COALESCE(endpoint, ''), COALESCE(method, ''), COALESCE(status, 0), COALESCE(latency_ms, 0), COALESCE(ttfb_ms, 0), COALESCE(stream, 0), COALESCE(client_ip_hash, ''), COALESCE(api_key_hash, ''), COALESCE(model_requested, ''), COALESCE(model_returned, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(reasoning_tokens, 0), COALESCE(cached_tokens, 0), COALESCE(cache_creation_tokens, 0), COALESCE(total_tokens, 0), COALESCE(request_bytes, 0), COALESCE(response_bytes, 0), COALESCE(error, ''), COALESCE(endpoint_profile, ''), COALESCE(capture_mode, ''), COALESCE(metering_kind, ''), COALESCE(capture_outcome, ''), COALESCE(capture_reason, ''), COALESCE(error_class, ''), COALESCE(error_type, ''), COALESCE(error_code, ''), COALESCE(error_param, ''), COALESCE(error_message, ''), COALESCE(error_message_truncated, 0), COALESCE(model_returned_source, ''), COALESCE(usage_source, ''), COALESCE(terminal_event, ''), COALESCE(terminal_reason, ''), COALESCE(side_usage_event_id, 0), COALESCE((SELECT match_status FROM side_usage_events WHERE matched_request_usage_id = request_usage.id ORDER BY CASE WHEN match_status = 'conflict' THEN 0 ELSE 1 END, id DESC LIMIT 1), '')
		FROM request_usage
		WHERE created_at_unix >= ?
			AND EXISTS (
				SELECT 1 FROM image_usage iu
				WHERE iu.request_usage_id = request_usage.id
			)
		ORDER BY id DESC
		LIMIT ?
	`, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RequestRow
	for rows.Next() {
		var r RequestRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.RequestID, &r.Endpoint, &r.Method, &r.Status, &r.LatencyMs, &r.TTFBMs, &r.Stream, &r.ClientIPHash, &r.APIKeyHash, &r.ModelRequested, &r.ModelReturned, &r.InputTokens, &r.OutputTokens, &r.ReasoningTokens, &r.CachedTokens, &r.CacheCreationTokens, &r.TotalTokens, &r.RequestBytes, &r.ResponseBytes, &r.Error, &r.EndpointProfile, &r.CaptureMode, &r.MeteringKind, &r.CaptureOutcome, &r.CaptureReason, &r.ErrorClass, &r.ErrorType, &r.ErrorCode, &r.ErrorParam, &r.ErrorMessage, &r.ErrorMessageTruncated, &r.ModelReturnedSource, &r.UsageSource, &r.TerminalEvent, &r.TerminalReason, &r.SideUsageEventID, &r.SideUsageMatchStatus); err != nil {
			return nil, err
		}
		if r.ModelReturnedSource == "" && strings.TrimSpace(r.ModelReturned) != "" {
			r.ModelReturnedSource = "legacy"
		}
		if r.UsageSource == "" && hasUsageTokens(r.InputTokens, r.OutputTokens, r.ReasoningTokens, r.CachedTokens, r.CacheCreationTokens, r.TotalTokens) {
			r.UsageSource = "http_response"
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ErrorTimelineFromRequestsReport is the context-aware request-error timeline.
func (db *DB) ErrorTimelineFromRequestsReport(ctx context.Context, since time.Time) ([]ErrorTimelineRow, error) {
	rows, err := db.read.QueryContext(ctx, `
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

// LatestHealthReport is the context-aware latest health_metrics snapshot.
// No rows yields (nil, nil) so callers can distinguish empty from complete data.
// JSON responses still serialize latest_health as a zero object when nil.
func (db *DB) LatestHealthReport(ctx context.Context) (*HealthRow, error) {
	row := &HealthRow{}
	err := db.read.QueryRowContext(ctx, `
		SELECT COALESCE(timestamp, ''),
			COALESCE(queue_depth, 0),
			COALESCE(dropped_events_total, 0),
			COALESCE(parse_error_total, 0),
			COALESCE(db_write_error_total, 0),
			COALESCE(sse_line_skips_total, 0)
		FROM health_metrics ORDER BY id DESC LIMIT 1
	`).Scan(&row.Timestamp, &row.QueueDepth, &row.DroppedEvents, &row.ParseErrors, &row.DBErrors, &row.SSELineSkips)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// GatewayCapabilitiesReport is the context-aware gateway capability aggregate.
func (db *DB) GatewayCapabilitiesReport(ctx context.Context, since time.Time) ([]GatewayCapabilityRow, error) {
	rows, err := db.read.QueryContext(ctx, `
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

// CaptureOutcomeCountsReport returns capture outcome counts from one snapshot query.
func (db *DB) CaptureOutcomeCountsReport(ctx context.Context, since time.Time) (captured, skipped, failed int64, err error) {
	err = db.read.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN capture_outcome = 'captured' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN capture_outcome = 'skipped' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN capture_outcome = 'failed' THEN 1 ELSE 0 END), 0)
		FROM request_usage
		WHERE created_at_unix >= ?
	`, since.Unix()).Scan(&captured, &skipped, &failed)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("capture outcome counts: %w", err)
	}
	return captured, skipped, failed, nil
}

// SideUsageStatusCountsReport is the context-aware side-usage status histogram.
func (db *DB) SideUsageStatusCountsReport(ctx context.Context, since time.Time) (map[string]int64, error) {
	rows, err := db.read.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(match_status), ''), 'unknown'), COUNT(*)
		FROM side_usage_events
		WHERE received_at_unix >= ?
		GROUP BY 1
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		result[status] = count
	}
	return result, rows.Err()
}

// RecentQuotaRefreshEventsReport is the context-aware quota diagnostics list.
func (db *DB) RecentQuotaRefreshEventsReport(ctx context.Context, since time.Time, limit int) ([]QuotaRefreshEventRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.read.QueryContext(ctx, `
		SELECT id, checked_at, checked_at_unix, provider, credential_hash, phase, status, adapter_status, duration_ms, error_class, error_message, partial,
			probe_http_status, probe_endpoint, probe_error_class, api_call_reachable, provider_supported, details_json
		FROM quota_refresh_events
		WHERE checked_at_unix >= ?
		ORDER BY checked_at_unix DESC, id DESC
		LIMIT ?
	`, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []QuotaRefreshEventRow{}
	for rows.Next() {
		var r QuotaRefreshEventRow
		if err := rows.Scan(&r.ID, &r.CheckedAt, &r.CheckedAtUnix, &r.Provider, &r.CredentialHash, &r.Phase, &r.Status, &r.AdapterStatus, &r.DurationMs, &r.ErrorClass, &r.ErrorMessage, &r.Partial,
			&r.ProbeHTTPStatus, &r.ProbeEndpoint, &r.ProbeErrorClass, &r.APICallReachable, &r.ProviderSupported, &r.DetailsJSON); err != nil {
			return nil, err
		}
		r.DetailsJSON = defaultDetailsJSON(r.DetailsJSON)
		result = append(result, r)
	}
	return result, rows.Err()
}
