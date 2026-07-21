package event

import (
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

// EventToRecord converts a domain Event to a database UsageRecord.
func EventToRecord(e Event) db.UsageRecord {
	record := db.UsageRecord{
		CreatedAt:           e.Timestamp.UTC().Format(time.RFC3339),
		RequestID:           e.ID,
		Endpoint:            e.Path,
		Method:              e.Method,
		Status:              e.Status,
		LatencyMs:           e.LatencyMs,
		TTFBMs:              e.TTFBMs,
		Stream:              e.Stream,
		ClientIPHash:        e.ClientIPHash,
		APIKeyHash:          e.APIKeyHash,
		ModelRequested:      e.ModelRequested,
		ModelReturned:       e.ModelReturned,
		InputTokens:         e.InputTokens,
		OutputTokens:        e.OutputTokens,
		ReasoningTokens:     e.ReasoningTokens,
		CachedTokens:        e.CachedTokens,
		CacheCreationTokens: e.CacheCreationTokens,
		TotalTokens:         e.TotalTokens,
		RequestBytes:        e.RequestBytes,
		ResponseBytes:       e.ResponseBytes,
		Error:               e.Error,

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

		ModelReturnedSource: e.ModelReturnedSource,
		UsageSource:         e.UsageSource,
		TerminalEvent:       e.TerminalEvent,
		TerminalReason:      e.TerminalReason,
		SideUsageEventID:    e.SideUsageEventID,
	}
	if len(e.UsageDimensions) > 0 {
		record.UsageDimensions = make([]db.UsageDimensionRecord, 0, len(e.UsageDimensions))
		for _, d := range e.UsageDimensions {
			record.UsageDimensions = append(record.UsageDimensions, db.UsageDimensionRecord{
				RequestID:       e.ID,
				CreatedAt:       record.CreatedAt,
				EndpointProfile: valueOrDefault(d.EndpointProfile, e.EndpointProfile),
				Provider:        d.Provider,
				Model:           d.Model,
				Modality:        d.Modality,
				Channel:         d.Channel,
				Metric:          d.Metric,
				Direction:       d.Direction,
				Unit:            d.Unit,
				Amount:          d.Amount,
				UsageSource:     valueOrDefault(d.UsageSource, e.UsageSource),
				CaptureOutcome:  valueOrDefault(d.CaptureOutcome, e.CaptureOutcome),
				CaptureReason:   valueOrDefault(d.CaptureReason, e.CaptureReason),
				DetailsJSON:     valueOrDefault(d.DetailsJSON, "{}"),
			})
		}
	}
	if e.ImageUsage != nil {
		record.ImageUsage = &db.ImageUsageRecord{
			RequestID:         e.ID,
			CreatedAt:         record.CreatedAt,
			Operation:         e.ImageUsage.Operation,
			Provider:          e.ImageUsage.Provider,
			ModelRequested:    valueOrDefault(e.ImageUsage.ModelRequested, e.ModelRequested),
			ModelReturned:     valueOrDefault(e.ImageUsage.ModelReturned, e.ModelReturned),
			Size:              e.ImageUsage.Size,
			Quality:           e.ImageUsage.Quality,
			OutputFormat:      e.ImageUsage.OutputFormat,
			Stream:            e.ImageUsage.Stream,
			ImageCount:        e.ImageUsage.ImageCount,
			PartialImageCount: e.ImageUsage.PartialImageCount,
			InputImageCount:   e.ImageUsage.InputImageCount,
			HasMask:           e.ImageUsage.HasMask,
			UsageSource:       valueOrDefault(e.ImageUsage.UsageSource, e.UsageSource),
			CaptureOutcome:    valueOrDefault(e.ImageUsage.CaptureOutcome, e.CaptureOutcome),
			CaptureReason:     valueOrDefault(e.ImageUsage.CaptureReason, e.CaptureReason),
			MetadataJSON:      valueOrDefault(e.ImageUsage.MetadataJSON, "{}"),
		}
	}
	return record
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
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
		LatestErrorClass:    row.LatestErrorClass,
		LatestErrorCode:     row.LatestErrorCode,
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
			CacheCreationTokens:   r.CacheCreationTokens,
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
			ModelReturnedSource:   r.ModelReturnedSource,
			UsageSource:           r.UsageSource,
			TerminalEvent:         r.TerminalEvent,
			TerminalReason:        r.TerminalReason,
			SideUsageEventID:      r.SideUsageEventID,
			SideUsageMatchStatus:  r.SideUsageMatchStatus,
			UsageConfidence:       usageConfidence(r),
		}
	}
	return result
}

// usageConfidence computes a confidence label from capture fields per the
// design (4.7 节). The states, in priority order:
//   - conflict: HTTP and side-channel usage disagree (needs manual attention)
//   - side_channel: usage supplemented by CPA usage queue
//   - observed: HTTP response/stream directly captured usage
//   - request_only: endpoint only records request facts (not complete usage)
//   - unsupported: passthrough profile, no usage extraction attempted
//   - missing_usage: usage-metered endpoint but usage was not captured
func usageConfidence(r db.RequestRow) string {
	if r.SideUsageMatchStatus == "conflict" {
		return "conflict"
	}
	if r.UsageSource == UsageSourceCliproxySide {
		return "side_channel"
	}
	// request_only and passthrough profiles do not produce observed usage even
	// when capture_outcome is "captured" (the reason is request_only_profile).
	if r.CaptureMode == CaptureRequestOnly {
		return "request_only"
	}
	if r.CaptureMode == CapturePassthrough {
		return "unsupported"
	}
	if r.CaptureOutcome == OutcomeCaptured {
		return "observed"
	}
	return "missing_usage"
}

// ErrorTimelineFromDB converts db.ErrorTimelineRow slice to domain ErrorTimelineReport slice.
func ErrorTimelineFromDB(rows []db.ErrorTimelineRow) []ErrorTimelineReport {
	result := make([]ErrorTimelineReport, len(rows))
	for i, r := range rows {
		result[i] = ErrorTimelineReport{
			Timestamp:       r.Timestamp,
			Count:           r.Count,
			ParseErrors:     r.ParseErrors,
			DBErrors:        r.DBErrors,
			DroppedEvents:   r.DroppedEvents,
			BaselineMissing: r.BaselineMissing,
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
