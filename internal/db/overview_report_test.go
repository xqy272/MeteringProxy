package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOverviewReportSnapshotIsConsistentAndTyped(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.InsertBatch([]UsageRecord{
		{
			CreatedAt: now.Add(-90 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/chat/completions", Method: "POST", Status: 200,
			LatencyMs: 100, TTFBMs: 20, ModelReturned: "known",
			InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
			CaptureMode: "usage_metered", CaptureOutcome: "captured", UsageSource: "http_response",
		},
		{
			CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
			RequestID: "failed-recent", Endpoint: "/v1/responses", Method: "POST", Status: 500,
			LatencyMs: 300, TTFBMs: 80, ModelRequested: "unknown-model",
			InputTokens: 20, OutputTokens: 4, TotalTokens: 24,
			CaptureMode: "usage_metered", CaptureOutcome: "failed",
			ErrorClass: "upstream_5xx", ErrorCode: "server_error",
		},
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/messages", Method: "POST", Status: 200,
			LatencyMs: 200, TTFBMs: 40, ModelReturned: "known",
			CaptureMode: "request_only", CaptureOutcome: "skipped",
		},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	snapshot, err := d.OverviewReportSnapshot(context.Background(), now.Add(-2*time.Hour), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("OverviewReportSnapshot: %v", err)
	}
	if snapshot.Selected.TotalRequests != 3 || snapshot.Selected.FailedRequests != 1 || snapshot.Selected.TotalTokens != 39 {
		t.Fatalf("selected = %+v", snapshot.Selected)
	}
	if snapshot.Selected.P95LatencyMs != 300 || snapshot.Selected.P95TTFBMs != 80 {
		t.Fatalf("selected percentiles = latency %d ttfb %d", snapshot.Selected.P95LatencyMs, snapshot.Selected.P95TTFBMs)
	}
	if snapshot.Recent.TotalRequests != 2 || snapshot.Recent.FailedRequests != 1 || snapshot.Recent.P95LatencyMs != 300 {
		t.Fatalf("recent = %+v", snapshot.Recent)
	}
	if snapshot.Recent.LatestError == nil || snapshot.Recent.LatestError.RequestID != "failed-recent" || snapshot.Recent.LatestError.Class != "upstream_5xx" {
		t.Fatalf("latest error = %+v", snapshot.Recent.LatestError)
	}
	if snapshot.CaptureFailed != 1 || snapshot.CaptureSkipped != 1 {
		t.Fatalf("capture failed=%d skipped=%d", snapshot.CaptureFailed, snapshot.CaptureSkipped)
	}
	var costRequests int64
	for _, row := range snapshot.TextCostBuckets {
		costRequests += row.RequestCount
	}
	if costRequests != snapshot.Selected.TotalRequests {
		t.Fatalf("cost request conservation = %d, selected = %d", costRequests, snapshot.Selected.TotalRequests)
	}
}

func TestOverviewReportSnapshotHonorsCanceledContext(t *testing.T) {
	d := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.OverviewReportSnapshot(ctx, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("OverviewReportSnapshot err=%v, want canceled", err)
	}
}
