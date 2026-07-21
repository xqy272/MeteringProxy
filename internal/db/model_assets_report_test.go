package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestModelAssetsReportSnapshotCarriesAllCostDimensions(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := d.InsertBatch([]UsageRecord{
		{
			CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/messages", Method: "POST", Status: 200,
			EndpointProfile: "anthropic_messages", CaptureMode: "usage_metered", CaptureOutcome: "captured",
			ModelRequested: "claude", ModelReturned: "claude", ModelReturnedSource: "response_body",
			InputTokens: 100, OutputTokens: 50, ReasoningTokens: 10,
			CachedTokens: 20, CacheCreationTokens: 5, TotalTokens: 150,
		},
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/responses", Method: "POST", Status: 500,
			EndpointProfile: "openai_responses", CaptureMode: "request_only", CaptureOutcome: "skipped",
			ModelRequested: "claude",
		},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	snapshot, err := d.ModelAssetsReportSnapshot(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ModelAssetsReportSnapshot: %v", err)
	}
	if len(snapshot.Rows) != 1 {
		t.Fatalf("rows = %+v", snapshot.Rows)
	}
	row := snapshot.Rows[0]
	if row.Model != "claude" || row.RequestCount != 2 || row.FailedCount != 1 {
		t.Fatalf("row = %+v", row)
	}
	if row.InputTokens != 100 || row.OutputTokens != 50 || row.ReasoningTokens != 10 || row.CachedTokens != 20 || row.CacheCreationTokens != 5 || row.TotalTokens != 150 {
		t.Fatalf("token dimensions = %+v", row)
	}
	if !strings.Contains(row.EndpointProfiles, "anthropic_messages") || !strings.Contains(row.EndpointProfiles, "openai_responses") {
		t.Fatalf("endpoint profiles = %q", row.EndpointProfiles)
	}
	if !strings.Contains(row.CaptureModes, "usage_metered") || !strings.Contains(row.CaptureModes, "request_only") {
		t.Fatalf("capture modes = %q", row.CaptureModes)
	}
	if !strings.Contains(row.ModelSources, "returned") || !strings.Contains(row.ModelSources, "requested") {
		t.Fatalf("model sources = %q", row.ModelSources)
	}
	var costRequests int64
	for _, bucket := range snapshot.TextCostBuckets {
		costRequests += bucket.RequestCount
	}
	if costRequests != row.RequestCount {
		t.Fatalf("cost requests=%d row requests=%d", costRequests, row.RequestCount)
	}
}

func TestModelAssetsReportSnapshotHonorsCanceledContext(t *testing.T) {
	d := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.ModelAssetsReportSnapshot(ctx, time.Now().Add(-time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("ModelAssetsReportSnapshot err=%v, want canceled", err)
	}
}
