package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSummaryAndTimeseriesSnapshotsShareCostScope(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)
	if err := d.InsertBatch([]UsageRecord{
		{CreatedAt: now.Add(-70 * time.Minute).Format(time.RFC3339), Endpoint: `/v1/chat/completions`, Method: `POST`, Status: 200, ModelReturned: `model-a`, InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CaptureOutcome: `captured`},
		{CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), Endpoint: `/v1/chat/completions`, Method: `POST`, Status: 500, ModelReturned: `model-b`, InputTokens: 20, OutputTokens: 4, TotalTokens: 24, CaptureOutcome: `captured`},
	}); err != nil {
		t.Fatalf(`InsertBatch: %v`, err)
	}
	since := now.Add(-2 * time.Hour)

	summary, err := d.SummaryReportSnapshot(context.Background(), since)
	if err != nil {
		t.Fatalf(`SummaryReportSnapshot: %v`, err)
	}
	if summary.Summary.TotalRequests != 2 || summary.Summary.FailedRequests != 1 || summary.Summary.TotalTokens != 39 {
		t.Fatalf(`summary = %+v`, summary.Summary)
	}
	var summaryCostRequests int64
	for _, row := range summary.TextCostBuckets {
		summaryCostRequests += row.RequestCount
	}
	if summaryCostRequests != summary.Summary.TotalRequests {
		t.Fatalf(`summary cost requests=%d total=%d`, summaryCostRequests, summary.Summary.TotalRequests)
	}

	series, err := d.TimeseriesReportSnapshot(context.Background(), since, 60)
	if err != nil {
		t.Fatalf(`TimeseriesReportSnapshot: %v`, err)
	}
	if len(series.Rows) != 2 {
		t.Fatalf(`timeseries rows = %+v`, series.Rows)
	}
	usageCounts := make(map[string]int64)
	for _, row := range series.Rows {
		usageCounts[row.Timestamp] += row.Count
	}
	costCounts := make(map[string]int64)
	for _, row := range series.TextCostBuckets {
		costCounts[row.Bucket] += row.RequestCount
	}
	if len(costCounts) != len(usageCounts) {
		t.Fatalf(`cost buckets=%+v usage=%+v`, costCounts, usageCounts)
	}
	for bucket, count := range usageCounts {
		if costCounts[bucket] != count {
			t.Fatalf(`bucket %s cost requests=%d usage requests=%d`, bucket, costCounts[bucket], count)
		}
	}
}

func TestSummaryAndTimeseriesSnapshotsHonorCanceledContext(t *testing.T) {
	d := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.SummaryReportSnapshot(ctx, time.Now().Add(-time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf(`SummaryReportSnapshot err=%v, want canceled`, err)
	}
	if _, err := d.TimeseriesReportSnapshot(ctx, time.Now().Add(-time.Hour), 60); !errors.Is(err, context.Canceled) {
		t.Fatalf(`TimeseriesReportSnapshot err=%v, want canceled`, err)
	}
}
