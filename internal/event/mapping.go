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
		EndpointProfile:   e.EndpointProfile,
		CaptureMode:       e.CaptureMode,
		MeteringKind:      e.MeteringKind,
		UsageRawJSON:      e.UsageRawJSON,
		UsageRawTruncated: e.UsageRawTruncated,
		BillableInput:     e.BillableInput,
		BillableOutput:    e.BillableOutput,
		BillableTotal:     e.BillableTotal,
		BillableUnit:      e.BillableUnit,
		CaptureOutcome:    e.CaptureOutcome,
		CaptureReason:     e.CaptureReason,
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
			RequestCount:    r.RequestCount,
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
			Timestamp:   r.Timestamp,
			Count:       r.Count,
			InputTokens: r.InputTokens,
			OutputTokens: r.OutputTokens,
			TotalTokens: r.TotalTokens,
		}
	}
	return result
}
