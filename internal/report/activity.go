package report

import (
	"context"
	"fmt"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/metrics"
)

func (s *Service) Activity(ctx context.Context, filter ActivityFilter) (ActivityReport, error) {
	return observeReport(metrics.ReportActivity, func() (ActivityReport, error) {
		if s == nil {
			return ActivityReport{}, fmt.Errorf("report service is not configured")
		}
		row, err := s.activity.ActivityReport(ctx, db.ReportScope{Since: filter.Since, KeyHash: filter.KeyHash})
		if err != nil {
			return ActivityReport{}, err
		}
		if row == nil {
			return ActivityReport{}, nil
		}
		return ActivityReport{
			SampleSize: row.SampleSize, SuccessCount: row.SuccessCount, FailedCount: row.FailedCount,
			FailureRate: row.FailureRate, AvgLatencyMs: row.AvgLatencyMs, P95LatencyMs: row.P95LatencyMs,
			AvgTTFBMs: row.AvgTTFBMs, P95TTFBMs: row.P95TTFBMs,
			CaptureCaptured: row.CaptureCaptured, CaptureFailed: row.CaptureFailed, CaptureSkipped: row.CaptureSkipped,
			LatestErrorStatus: row.LatestErrorStatus, LatestErrorAt: row.LatestErrorAt,
			LatestError: row.LatestError, LatestErrorClass: row.LatestErrorClass, LatestErrorCode: row.LatestErrorCode,
			LatestErrorEndpoint: row.LatestErrorEndpoint, LatestErrorModel: row.LatestErrorModel,
		}, nil
	})
}
