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
