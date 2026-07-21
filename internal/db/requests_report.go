package db

import (
	"context"
	"strings"
)

type RequestFilter struct {
	Scope      ReportScope
	Limit      int
	StatusMin  int
	StatusMax  int
	Model      string
	Endpoint   string
	ErrorClass string
}

func (db *DB) RequestsReport(ctx context.Context, filter RequestFilter) ([]RequestRow, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	query := "SELECT id, COALESCE(created_at, ''), COALESCE(request_id, ''), COALESCE(endpoint, ''), COALESCE(method, ''), COALESCE(status, 0), COALESCE(latency_ms, 0), COALESCE(ttfb_ms, 0), COALESCE(stream, 0), COALESCE(client_ip_hash, ''), COALESCE(api_key_hash, ''), COALESCE(model_requested, ''), COALESCE(model_returned, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(reasoning_tokens, 0), COALESCE(cached_tokens, 0), COALESCE(cache_creation_tokens, 0), COALESCE(total_tokens, 0), COALESCE(request_bytes, 0), COALESCE(response_bytes, 0), COALESCE(error, ''), COALESCE(endpoint_profile, ''), COALESCE(capture_mode, ''), COALESCE(metering_kind, ''), COALESCE(capture_outcome, ''), COALESCE(capture_reason, ''), COALESCE(error_class, ''), COALESCE(error_type, ''), COALESCE(error_code, ''), COALESCE(error_param, ''), COALESCE(error_message, ''), COALESCE(error_message_truncated, 0), COALESCE(model_returned_source, ''), COALESCE(usage_source, ''), COALESCE(terminal_event, ''), COALESCE(terminal_reason, ''), COALESCE(side_usage_event_id, 0), COALESCE((SELECT match_status FROM side_usage_events WHERE matched_request_usage_id = request_usage.id ORDER BY CASE WHEN match_status = 'conflict' THEN 0 ELSE 1 END, id DESC LIMIT 1), '') FROM request_usage WHERE 1=1"
	var args []any
	if !filter.Scope.Since.IsZero() {
		query += " AND created_at_unix >= ?"
		args = append(args, filter.Scope.Since.Unix())
	}
	switch {
	case filter.Scope.KeyHash == "":
	case filter.Scope.KeyHash == "unknown":
		query += " AND NULLIF(TRIM(api_key_hash), '') IS NULL"
	default:
		query += " AND api_key_hash = ?"
		args = append(args, filter.Scope.KeyHash)
	}
	if filter.StatusMin > 0 {
		query += " AND status >= ?"
		args = append(args, filter.StatusMin)
	}
	if filter.StatusMax > 0 {
		query += " AND status < ?"
		args = append(args, filter.StatusMax)
	}
	if filter.Model != "" {
		query += " AND " + effectiveModelExpr + " = ?"
		args = append(args, filter.Model)
	}
	if filter.Endpoint != "" {
		if profileName, ok := strings.CutPrefix(filter.Endpoint, "profile:"); ok {
			query += " AND endpoint_profile = ?"
			args = append(args, profileName)
		} else {
			query += " AND endpoint = ?"
			args = append(args, filter.Endpoint)
		}
	}
	if filter.ErrorClass != "" {
		query += " AND error_class = ?"
		args = append(args, filter.ErrorClass)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, filter.Limit)

	rows, err := db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RequestRow, 0)
	for rows.Next() {
		var row RequestRow
		if err := rows.Scan(&row.ID, &row.CreatedAt, &row.RequestID, &row.Endpoint, &row.Method, &row.Status, &row.LatencyMs, &row.TTFBMs, &row.Stream, &row.ClientIPHash, &row.APIKeyHash, &row.ModelRequested, &row.ModelReturned, &row.InputTokens, &row.OutputTokens, &row.ReasoningTokens, &row.CachedTokens, &row.CacheCreationTokens, &row.TotalTokens, &row.RequestBytes, &row.ResponseBytes, &row.Error, &row.EndpointProfile, &row.CaptureMode, &row.MeteringKind, &row.CaptureOutcome, &row.CaptureReason, &row.ErrorClass, &row.ErrorType, &row.ErrorCode, &row.ErrorParam, &row.ErrorMessage, &row.ErrorMessageTruncated, &row.ModelReturnedSource, &row.UsageSource, &row.TerminalEvent, &row.TerminalReason, &row.SideUsageEventID, &row.SideUsageMatchStatus); err != nil {
			return nil, err
		}
		if row.ModelReturnedSource == "" && strings.TrimSpace(row.ModelReturned) != "" {
			row.ModelReturnedSource = "legacy"
		}
		if row.UsageSource == "" && hasUsageTokens(row.InputTokens, row.OutputTokens, row.ReasoningTokens, row.CachedTokens, row.CacheCreationTokens, row.TotalTokens) {
			row.UsageSource = "http_response"
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
