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

	series, err := d.TimeseriesReportSnapshot(context.Background(), ReportScope{Since: since}, 60)
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
	if _, err := d.TimeseriesReportSnapshot(ctx, ReportScope{Since: time.Now().Add(-time.Hour)}, 60); !errors.Is(err, context.Canceled) {
		t.Fatalf(`TimeseriesReportSnapshot err=%v, want canceled`, err)
	}
}

func TestModelsAndTimeseriesSnapshotsFilterExactKey(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)
	keyA := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	keyB := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := d.InsertBatch([]UsageRecord{
		{CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), APIKeyHash: keyA, Endpoint: "/v1/chat/completions", Method: "POST", Status: 200, ModelReturned: "model-a", InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CaptureOutcome: "captured"},
		{CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), APIKeyHash: keyA, Endpoint: "/v1/responses", Method: "POST", Status: 500, ModelReturned: "model-b", InputTokens: 20, OutputTokens: 4, TotalTokens: 24, CaptureOutcome: "captured"},
		{CreatedAt: now.Add(-5 * time.Minute).Format(time.RFC3339), APIKeyHash: keyB, Endpoint: "/v1/messages", Method: "POST", Status: 200, ModelReturned: "model-b", InputTokens: 30, OutputTokens: 3, TotalTokens: 33, CaptureOutcome: "captured"},
		{CreatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339), Endpoint: "/v1/chat/completions", Method: "POST", Status: 200, ModelReturned: "model-unknown-key", InputTokens: 40, OutputTokens: 2, TotalTokens: 42, CaptureOutcome: "captured"},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	since := now.Add(-time.Hour)

	models, err := d.ModelsReportSnapshot(context.Background(), ReportScope{Since: since, KeyHash: keyA})
	if err != nil {
		t.Fatalf("ModelsReportSnapshot key A: %v", err)
	}
	if len(models.Models) != 2 {
		t.Fatalf("key A models = %+v", models.Models)
	}
	var modelRequests, costRequests int64
	for _, row := range models.Models {
		modelRequests += row.RequestCount
	}
	for _, row := range models.TextCostBuckets {
		costRequests += row.RequestCount
	}
	if modelRequests != 2 || costRequests != 2 {
		t.Fatalf("key A request conservation models=%d cost=%d", modelRequests, costRequests)
	}
	if _, ok := models.ModelReturnedSourceCounts["model-unknown-key"]; ok {
		t.Fatalf("source counts leaked unknown key: %+v", models.ModelReturnedSourceCounts)
	}

	unknown, err := d.ModelsReportSnapshot(context.Background(), ReportScope{Since: since, KeyHash: "unknown"})
	if err != nil {
		t.Fatalf("ModelsReportSnapshot unknown: %v", err)
	}
	if len(unknown.Models) != 1 || unknown.Models[0].Model != "model-unknown-key" || unknown.Models[0].RequestCount != 1 {
		t.Fatalf("unknown models = %+v", unknown.Models)
	}

	series, err := d.TimeseriesReportSnapshot(context.Background(), ReportScope{Since: since, KeyHash: keyB}, 60)
	if err != nil {
		t.Fatalf("TimeseriesReportSnapshot key B: %v", err)
	}
	if len(series.Rows) != 1 || series.Rows[0].Count != 1 || series.Rows[0].TotalTokens != 33 {
		t.Fatalf("key B timeseries = %+v", series.Rows)
	}
	var seriesCostRequests int64
	for _, row := range series.TextCostBuckets {
		seriesCostRequests += row.RequestCount
	}
	if seriesCostRequests != 1 {
		t.Fatalf("key B cost requests = %d", seriesCostRequests)
	}
}
