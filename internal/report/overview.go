package report

import (
	"context"
	"fmt"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

func (s *Service) Overview(ctx context.Context, filter OverviewFilter) (OverviewReport, error) {
	if s == nil {
		return OverviewReport{}, fmt.Errorf("report service is not configured")
	}
	recentSince := s.now().Add(-time.Hour)
	snapshot, err := s.overview.OverviewReportSnapshot(ctx, filter.Since, recentSince)
	if err != nil {
		return OverviewReport{}, err
	}
	cost := completeZeroCost()
	if value, ok := evaluateCostBuckets(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, 0)[CostGroup{}]; ok {
		cost = value
	}
	queueDepth, dropped, parseErrors, dbErrors := s.capture.Snapshot()

	selectedStatus := SectionStatusComplete
	costStatus := SectionStatusComplete
	if cost.State == CostStatePartial {
		selectedStatus = SectionStatusPartial
		costStatus = SectionStatusPartial
	} else if cost.State == CostStateUnavailable {
		selectedStatus = SectionStatusPartial
		costStatus = SectionStatusUnavailable
	}
	captureState := "healthy"
	if queueDepth > 0 || dropped > 0 || parseErrors > 0 || dbErrors > 0 || snapshot.CaptureFailed > 0 || snapshot.CaptureSkipped > 0 {
		captureState = "attention"
	}

	return OverviewReport{
		Range: filter.Range,
		Selected: OverviewSelectedSection{
			Data: OverviewSelectedData{
				TotalRequests: snapshot.Selected.TotalRequests, FailedRequests: snapshot.Selected.FailedRequests,
				FailureRate:      failureRate(snapshot.Selected.FailedRequests, snapshot.Selected.TotalRequests),
				TotalInputTokens: snapshot.Selected.TotalInputTokens, TotalOutputTokens: snapshot.Selected.TotalOutputTokens,
				TotalReasoningTokens: snapshot.Selected.TotalReasoningTokens, TotalCachedTokens: snapshot.Selected.TotalCachedTokens,
				TotalTokens: snapshot.Selected.TotalTokens, TotalCost: cost.Amount,
				P95LatencyMs: snapshot.Selected.P95LatencyMs, P95TTFBMs: snapshot.Selected.P95TTFBMs,
			},
			Status: selectedStatus,
		},
		Recent1h: OverviewRecentSection{
			Data: OverviewRecentData{
				TotalRequests: snapshot.Recent.TotalRequests, FailedRequests: snapshot.Recent.FailedRequests,
				FailureRate:  failureRate(snapshot.Recent.FailedRequests, snapshot.Recent.TotalRequests),
				P95LatencyMs: snapshot.Recent.P95LatencyMs, LatestError: overviewLatestError(snapshot.Recent.LatestError),
			},
			Status: SectionStatusComplete,
		},
		Capture: OverviewCaptureSection{
			Data: OverviewCaptureData{
				Status: captureState, QueueDepth: queueDepth, DroppedEvents: dropped,
				ParseErrors: parseErrors, DBWriteErrors: dbErrors,
				CaptureFailed: snapshot.CaptureFailed, CaptureSkipped: snapshot.CaptureSkipped,
			},
			Status: SectionStatusComplete,
		},
		Cost: OverviewCostSection{
			Data: OverviewCostData{
				KnownCost: cost.Amount, UnpricedModels: cost.UnpricedModels,
				Partial: cost.State != CostStateComplete, CostKnown: cost.CostKnown, CostState: cost.State,
				PartialReasons:        nonNilPartialReasons(cost.PartialReasons),
				UsageConfidenceCounts: cost.UsageConfidenceCounts,
			},
			Status: costStatus,
		},
	}, nil
}

func failureRate(failed, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(failed) / float64(total)
}

func overviewLatestError(row *db.OverviewLatestErrorRow) *OverviewLatestError {
	if row == nil {
		return nil
	}
	return &OverviewLatestError{
		LatestAt: row.LatestAt, Status: row.Status, Endpoint: row.Endpoint,
		Model: row.Model, ModelSource: row.ModelSource, Class: row.Class,
		Message: row.Message, RequestID: row.RequestID,
	}
}
