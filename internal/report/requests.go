package report

import (
	"context"
	"fmt"

	"ai-gateway-metering-proxy/internal/db"
)

func (s *Service) Requests(ctx context.Context, filter RequestFilter) ([]RequestReport, error) {
	if s == nil {
		return nil, fmt.Errorf("report service is not configured")
	}
	rows, err := s.requests.RequestsReport(ctx, db.RequestFilter{
		Scope: db.ReportScope{Since: filter.Since, KeyHash: filter.KeyHash},
		Limit: filter.Limit, StatusMin: filter.StatusMin, StatusMax: filter.StatusMax,
		Model: filter.Model, Endpoint: filter.Endpoint, ErrorClass: filter.ErrorClass,
	})
	if err != nil {
		return nil, err
	}
	out := make([]RequestReport, len(rows))
	for i, row := range rows {
		out[i] = requestReportFromRow(row)
	}
	return out, nil
}

func requestReportFromRow(row db.RequestRow) RequestReport {
	return RequestReport{
		ID: row.ID, CreatedAt: row.CreatedAt, RequestID: row.RequestID,
		Endpoint: row.Endpoint, EndpointProfile: row.EndpointProfile,
		CaptureMode: row.CaptureMode, MeteringKind: row.MeteringKind,
		Method: row.Method, Status: row.Status, LatencyMs: row.LatencyMs, TTFBMs: row.TTFBMs,
		Stream: row.Stream, ClientIPHash: row.ClientIPHash, APIKeyHash: row.APIKeyHash,
		ModelRequested: row.ModelRequested, ModelReturned: row.ModelReturned,
		InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, ReasoningTokens: row.ReasoningTokens,
		CachedTokens: row.CachedTokens, CacheCreationTokens: row.CacheCreationTokens, TotalTokens: row.TotalTokens,
		RequestBytes: row.RequestBytes, ResponseBytes: row.ResponseBytes,
		CaptureOutcome: row.CaptureOutcome, CaptureReason: row.CaptureReason,
		Error: row.Error, ErrorClass: row.ErrorClass, ErrorType: row.ErrorType,
		ErrorCode: row.ErrorCode, ErrorParam: row.ErrorParam, ErrorMessage: row.ErrorMessage,
		ErrorMessageTruncated: row.ErrorMessageTruncated, ModelReturnedSource: row.ModelReturnedSource,
		UsageSource: row.UsageSource, TerminalEvent: row.TerminalEvent, TerminalReason: row.TerminalReason,
		SideUsageEventID: row.SideUsageEventID, SideUsageMatchStatus: row.SideUsageMatchStatus,
		UsageConfidence: requestUsageConfidence(row),
	}
}

func requestUsageConfidence(row db.RequestRow) string {
	switch {
	case row.SideUsageMatchStatus == "conflict":
		return "conflict"
	case row.UsageSource == "cliproxy_side_channel":
		return "side_channel"
	case row.CaptureMode == "request_only":
		return "request_only"
	case row.CaptureMode == "passthrough":
		return "unsupported"
	case row.CaptureOutcome == "captured":
		return "observed"
	default:
		return "missing_usage"
	}
}
