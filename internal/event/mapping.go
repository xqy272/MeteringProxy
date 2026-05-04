package event

import (
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

// EventToRecord converts a domain Event to a database UsageRecord.
func EventToRecord(e Event) db.UsageRecord {
	return db.UsageRecord{
		CreatedAt:       e.Timestamp.UTC().Format(time.RFC3339),
		RequestID:       e.ID,
		Endpoint:        e.Path,
		Method:          e.Method,
		Status:          e.Status,
		LatencyMs:       e.LatencyMs,
		TTFBMs:          e.TTFBMs,
		Stream:          e.Stream,
		ClientIPHash:    e.ClientIPHash,
		APIKeyHash:      e.APIKeyHash,
		ModelRequested:  e.ModelRequested,
		ModelReturned:   e.ModelReturned,
		InputTokens:     e.InputTokens,
		OutputTokens:    e.OutputTokens,
		ReasoningTokens: e.ReasoningTokens,
		CachedTokens:    e.CachedTokens,
		TotalTokens:     e.TotalTokens,
		RequestBytes:    e.RequestBytes,
		ResponseBytes:   e.ResponseBytes,
		Error:           e.Error,

		// W3 extended fields
		EndpointProfile:       e.EndpointProfile,
		CaptureMode:           e.CaptureMode,
		MeteringKind:          e.MeteringKind,
		UsageRawJSON:          e.UsageRawJSON,
		UsageRawTruncated:     e.UsageRawTruncated,
		BillableInput:         e.BillableInput,
		BillableOutput:        e.BillableOutput,
		BillableTotal:         e.BillableTotal,
		BillableUnit:          e.BillableUnit,
		CaptureOutcome:        e.CaptureOutcome,
		CaptureReason:         e.CaptureReason,
		ErrorClass:            e.ErrorClass,
		ErrorType:             e.ErrorType,
		ErrorCode:             e.ErrorCode,
		ErrorParam:            e.ErrorParam,
		ErrorMessage:          e.ErrorMessage,
		ErrorMessageTruncated: e.ErrorMessageTruncated,
	}
}

// SummaryFromDB converts a db.SummaryRow to a domain SummaryReport.
func SummaryFromDB(row *db.SummaryRow) SummaryReport {
	if row == nil {
		return SummaryReport{}
	}
	return SummaryReport{
		TotalRequests:        row.TotalRequests,
		FailedRequests:       row.FailedRequests,
		TotalInputTokens:     row.TotalInputTokens,
		TotalOutputTokens:    row.TotalOutputTokens,
		TotalReasoningTokens: row.TotalReasoningTokens,
		TotalCachedTokens:    row.TotalCachedTokens,
		TotalTokens:          row.TotalTokens,
		TotalCost:            row.TotalCost,
	}
}

// ModelsFromDB converts db.ModelRow slice to domain ModelReport slice.
func ModelsFromDB(rows []db.ModelRow) []ModelReport {
	result := make([]ModelReport, len(rows))
	for i, r := range rows {
		result[i] = ModelReport{
			Model:           r.Model,
			ModelSource:     r.ModelSource,
			RequestCount:    r.RequestCount,
			FailedCount:     r.FailedCount,
			InputTokens:     r.InputTokens,
			OutputTokens:    r.OutputTokens,
			ReasoningTokens: r.ReasoningTokens,
			CachedTokens:    r.CachedTokens,
			TotalTokens:     r.TotalTokens,
		}
	}
	return result
}

// KeysFromDB converts db.KeyRow slice to domain KeyReport slice.
func KeysFromDB(rows []db.KeyRow) []KeyReport {
	result := make([]KeyReport, len(rows))
	for i, r := range rows {
		result[i] = KeyReport{
			KeyHash:      r.KeyHash,
			RequestCount: r.RequestCount,
			FailedCount:  r.FailedCount,
			InputTokens:  r.InputTokens,
			OutputTokens: r.OutputTokens,
			TotalTokens:  r.TotalTokens,
		}
	}
	return result
}

// TimeseriesFromDB converts db.TimeseriesRow slice to domain TimeseriesReport slice.
func TimeseriesFromDB(rows []db.TimeseriesRow) []TimeseriesReport {
	result := make([]TimeseriesReport, len(rows))
	for i, r := range rows {
		result[i] = TimeseriesReport{
			Timestamp:       r.Timestamp,
			Count:           r.Count,
			FailedCount:     r.FailedCount,
			InputTokens:     r.InputTokens,
			OutputTokens:    r.OutputTokens,
			ReasoningTokens: r.ReasoningTokens,
			CachedTokens:    r.CachedTokens,
			TotalTokens:     r.TotalTokens,
			AvgLatencyMs:    r.AvgLatencyMs,
			AvgTTFBMs:       r.AvgTTFBMs,
		}
	}
	return result
}

func ActivityFromDB(row *db.ActivityRow) ActivityReport {
	if row == nil {
		return ActivityReport{}
	}
	return ActivityReport{
		SampleSize:          row.SampleSize,
		SuccessCount:        row.SuccessCount,
		FailedCount:         row.FailedCount,
		FailureRate:         row.FailureRate,
		AvgLatencyMs:        row.AvgLatencyMs,
		P95LatencyMs:        row.P95LatencyMs,
		AvgTTFBMs:           row.AvgTTFBMs,
		P95TTFBMs:           row.P95TTFBMs,
		CaptureCaptured:     row.CaptureCaptured,
		CaptureFailed:       row.CaptureFailed,
		CaptureSkipped:      row.CaptureSkipped,
		LatestErrorStatus:   row.LatestErrorStatus,
		LatestErrorAt:       row.LatestErrorAt,
		LatestError:         row.LatestError,
		LatestErrorEndpoint: row.LatestErrorEndpoint,
		LatestErrorModel:    row.LatestErrorModel,
	}
}

// RequestsFromDB converts db.RequestRow slice to domain RequestReport slice.
func RequestsFromDB(rows []db.RequestRow) []RequestReport {
	result := make([]RequestReport, len(rows))
	for i, r := range rows {
		result[i] = RequestReport{
			ID:                    r.ID,
			CreatedAt:             r.CreatedAt,
			RequestID:             r.RequestID,
			Endpoint:              r.Endpoint,
			EndpointProfile:       r.EndpointProfile,
			CaptureMode:           r.CaptureMode,
			MeteringKind:          r.MeteringKind,
			Method:                r.Method,
			Status:                r.Status,
			LatencyMs:             r.LatencyMs,
			TTFBMs:                r.TTFBMs,
			Stream:                r.Stream,
			ClientIPHash:          r.ClientIPHash,
			APIKeyHash:            r.APIKeyHash,
			ModelRequested:        r.ModelRequested,
			ModelReturned:         r.ModelReturned,
			InputTokens:           r.InputTokens,
			OutputTokens:          r.OutputTokens,
			ReasoningTokens:       r.ReasoningTokens,
			CachedTokens:          r.CachedTokens,
			TotalTokens:           r.TotalTokens,
			RequestBytes:          r.RequestBytes,
			ResponseBytes:         r.ResponseBytes,
			CaptureOutcome:        r.CaptureOutcome,
			CaptureReason:         r.CaptureReason,
			Error:                 r.Error,
			ErrorClass:            r.ErrorClass,
			ErrorType:             r.ErrorType,
			ErrorCode:             r.ErrorCode,
			ErrorParam:            r.ErrorParam,
			ErrorMessage:          r.ErrorMessage,
			ErrorMessageTruncated: r.ErrorMessageTruncated,
		}
	}
	return result
}

// ErrorTimelineFromDB converts db.ErrorTimelineRow slice to domain ErrorTimelineReport slice.
func ErrorTimelineFromDB(rows []db.ErrorTimelineRow) []ErrorTimelineReport {
	result := make([]ErrorTimelineReport, len(rows))
	for i, r := range rows {
		result[i] = ErrorTimelineReport{
			Timestamp:     r.Timestamp,
			Count:         r.Count,
			ParseErrors:   r.ParseErrors,
			DBErrors:      r.DBErrors,
			DroppedEvents: r.DroppedEvents,
		}
	}
	return result
}

// HealthFromDB converts a db.HealthRow to a domain HealthReport.
func HealthFromDB(row *db.HealthRow) HealthReport {
	if row == nil {
		return HealthReport{}
	}
	return HealthReport{
		Timestamp:     row.Timestamp,
		QueueDepth:    row.QueueDepth,
		DroppedEvents: row.DroppedEvents,
		ParseErrors:   row.ParseErrors,
		DBErrors:      row.DBErrors,
		SSELineSkips:  row.SSELineSkips,
	}
}
