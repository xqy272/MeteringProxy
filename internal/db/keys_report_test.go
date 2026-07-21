package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeysReportSnapshotGroupsUnknownAndConservesCostScope(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	keyA := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	keyB := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := d.InsertBatch([]UsageRecord{
		{
			CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), APIKeyHash: keyA,
			Endpoint: "/v1/chat/completions", Method: "POST", Status: 200,
			LatencyMs: 100, TTFBMs: 20, ModelReturned: "model-a",
			InputTokens: 100, OutputTokens: 50, ReasoningTokens: 5,
			CachedTokens: 20, CacheCreationTokens: 3, TotalTokens: 150,
			CaptureMode: "usage_metered", CaptureOutcome: "captured",
		},
		{
			CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), APIKeyHash: keyA,
			Endpoint: "/v1/responses", Method: "POST", Status: 500,
			LatencyMs: 300, TTFBMs: 80, ModelReturned: "model-b",
			InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
			CaptureMode: "usage_metered", CaptureOutcome: "failed",
		},
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), APIKeyHash: keyB,
			Endpoint: "/v1/messages", Method: "POST", Status: 200,
			LatencyMs: 200, TTFBMs: 40, ModelReturned: "model-a",
			InputTokens: 20, OutputTokens: 10, TotalTokens: 30,
			CaptureMode: "usage_metered", CaptureOutcome: "captured",
		},
		{
			CreatedAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/images/generations", Method: "POST", Status: 200,
			LatencyMs: 50, ModelReturned: "image-model",
			CaptureMode: "usage_metered", CaptureOutcome: "captured",
			ImageUsage: &ImageUsageRecord{Operation: "generation", ModelReturned: "image-model", Size: "1024x1024", ImageCount: 1, InputImageCount: 2},
		},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	snapshot, err := d.KeysReportSnapshot(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("KeysReportSnapshot: %v", err)
	}
	if len(snapshot.Rows) != 3 {
		t.Fatalf("rows = %+v", snapshot.Rows)
	}
	byKey := make(map[string]KeyRow)
	for _, row := range snapshot.Rows {
		byKey[row.KeyHash] = row
	}
	a := byKey[keyA]
	if a.RequestCount != 2 || a.FailedCount != 1 || a.ModelCount != 2 || a.InputTokens != 110 || a.OutputTokens != 55 || a.ReasoningTokens != 5 || a.CachedTokens != 20 || a.CacheCreationTokens != 3 || a.TotalTokens != 165 {
		t.Fatalf("key A = %+v", a)
	}
	if a.AvgLatencyMs != 200 || a.AvgTTFBMs != 50 || a.LatestSeenAt == "" {
		t.Fatalf("key A timing = %+v", a)
	}
	if byKey[keyB].RequestCount != 1 || byKey["unknown"].RequestCount != 1 {
		t.Fatalf("key B/unknown = %+v / %+v", byKey[keyB], byKey["unknown"])
	}

	costRequests := make(map[string]int64)
	for _, bucket := range snapshot.TextCostBuckets {
		costRequests[bucket.KeyHash] += bucket.RequestCount
	}
	if costRequests[keyA] != 2 || costRequests[keyB] != 1 || costRequests["unknown"] != 1 {
		t.Fatalf("text cost requests = %+v", costRequests)
	}
	if len(snapshot.ImageCostBuckets) != 1 || snapshot.ImageCostBuckets[0].KeyHash != "unknown" || snapshot.ImageCostBuckets[0].InputImageCount != 2 {
		t.Fatalf("image cost buckets = %+v", snapshot.ImageCostBuckets)
	}
}

func TestKeysReportSnapshotHonorsCanceledContext(t *testing.T) {
	d := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.KeysReportSnapshot(ctx, time.Now().Add(-time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("KeysReportSnapshot err=%v, want canceled", err)
	}
}
