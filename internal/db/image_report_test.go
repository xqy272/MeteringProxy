package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestImageReportSnapshotGroupsOperationAndConserves(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	records := []UsageRecord{
		{
			CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), Status: 200,
			Endpoint: "/v1/images/generations", Method: "POST", ModelReturned: "gpt-image-2",
			InputTokens: 50, OutputTokens: 70, TotalTokens: 120,
			CaptureMode: "usage_metered", CaptureOutcome: "captured", UsageSource: "http_response",
			UsageDimensions: []UsageDimensionRecord{
				{Modality: "image", Channel: "text", Metric: "tokens", Direction: "input", Unit: "token", Amount: 10},
				{Modality: "image", Channel: "image", Metric: "tokens", Direction: "input", Unit: "token", Amount: 40},
				{Modality: "image", Channel: "image", Metric: "tokens", Direction: "output", Unit: "token", Amount: 70},
			},
			ImageUsage: &ImageUsageRecord{Operation: "generation", ModelReturned: "gpt-image-2", Size: "1024x1024", ImageCount: 2, InputImageCount: 1},
		},
		{
			CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), Status: 500,
			Endpoint: "/v1/images/edits", Method: "POST", ModelReturned: "grok-imagine-image-quality",
			CaptureMode: "usage_metered", CaptureOutcome: "captured", UsageSource: "http_response",
			ImageUsage: &ImageUsageRecord{Operation: "edit", ModelReturned: "grok-imagine-image-quality", Size: "2048x2048", ImageCount: 1, InputImageCount: 3},
		},
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), Status: 200,
			Endpoint: "/v1/images/generations", Method: "POST", ModelReturned: "grok-imagine-image-quality",
			CaptureMode: "usage_metered", CaptureOutcome: "captured", UsageSource: "http_response",
			ImageUsage: &ImageUsageRecord{Operation: "generation", ModelReturned: "grok-imagine-image-quality"},
		},
	}
	if err := d.InsertBatch(records); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	snapshot, err := d.ImageReportSnapshot(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ImageReportSnapshot: %v", err)
	}
	if snapshot.Summary.RequestCount != 3 || snapshot.Summary.FailedCount != 1 || snapshot.Summary.ImageCount != 3 || snapshot.Summary.InputImageCount != 4 {
		t.Fatalf("summary = %+v", snapshot.Summary)
	}
	if snapshot.Summary.TotalTokens != 120 {
		t.Fatalf("summary total tokens = %d, want 120", snapshot.Summary.TotalTokens)
	}

	var modelRequests, modelInputImages, modelOutputImages int64
	seenModels := make(map[string]bool)
	for _, row := range snapshot.Models {
		modelRequests += row.RequestCount
		modelInputImages += row.InputImageCount
		modelOutputImages += row.ImageCount
		seenModels[row.Model+"/"+row.Operation] = true
	}
	if modelRequests != 3 || modelInputImages != 4 || modelOutputImages != 3 {
		t.Fatalf("model conservation requests=%d input=%d output=%d rows=%+v", modelRequests, modelInputImages, modelOutputImages, snapshot.Models)
	}
	for _, key := range []string{"gpt-image-2/generation", "grok-imagine-image-quality/edit", "grok-imagine-image-quality/generation"} {
		if !seenModels[key] {
			t.Fatalf("missing model/operation %q in %+v", key, snapshot.Models)
		}
	}

	var textRequests int64
	seenTextOps := make(map[string]bool)
	for _, row := range snapshot.TextCostBuckets {
		textRequests += row.RequestCount
		seenTextOps[row.Operation] = true
		if !row.ImageRequest {
			t.Fatalf("image-only text bucket not marked image request: %+v", row)
		}
	}
	if textRequests != 3 || !seenTextOps["generation"] || !seenTextOps["edit"] {
		t.Fatalf("text buckets requests=%d operations=%+v rows=%+v", textRequests, seenTextOps, snapshot.TextCostBuckets)
	}

	var imageRequests, imageInputs, imageOutputs, missingOutputs int64
	seenSizes := make(map[string]bool)
	for _, row := range snapshot.ImageCostBuckets {
		imageRequests += row.RequestCount
		imageInputs += row.InputImageCount
		imageOutputs += row.OutputImageCount
		missingOutputs += row.MissingOutputCount
		seenSizes[row.Size] = true
	}
	if imageRequests != 3 || imageInputs != 4 || imageOutputs != 3 || missingOutputs != 1 {
		t.Fatalf("image buckets requests=%d input=%d output=%d missing=%d rows=%+v", imageRequests, imageInputs, imageOutputs, missingOutputs, snapshot.ImageCostBuckets)
	}
	if !seenSizes["1024x1024"] || !seenSizes["2048x2048"] || !seenSizes[""] {
		t.Fatalf("sizes = %+v", seenSizes)
	}
}

func TestImageReportSnapshotHonorsCanceledContext(t *testing.T) {
	d := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.ImageReportSnapshot(ctx, time.Now().Add(-time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("ImageReportSnapshot err=%v, want canceled", err)
	}
}
