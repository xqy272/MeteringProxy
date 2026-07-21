package db

import (
	"context"
	"testing"
	"time"
)

func TestCostBucketsPreserveTextTierAndImageDimensions(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	records := []UsageRecord{
		{
			CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), Endpoint: `/v1/chat/completions`, Method: `POST`, Status: 200,
			APIKeyHash: `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`, ModelReturned: `gemini-tier`,
			InputTokens: 150000, OutputTokens: 100, ReasoningTokens: 20, CachedTokens: 50000, CacheCreationTokens: 20000,
			CaptureMode: `usage_metered`, CaptureOutcome: `captured`, UsageSource: `http_response`,
		},
		{
			CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), Endpoint: `/v1/chat/completions`, Method: `POST`, Status: 200,
			APIKeyHash: `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`, ModelReturned: `gemini-tier`,
			InputTokens: 150000, OutputTokens: 50,
			CaptureMode: `usage_metered`, CaptureOutcome: `captured`, UsageSource: `http_response`,
		},
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), Endpoint: `/v1/chat/completions`, Method: `POST`, Status: 200,
			ModelReturned: `gemini-tier`, InputTokens: 200000, OutputTokens: 25,
			CaptureMode: `usage_metered`, CaptureOutcome: `captured`, UsageSource: `http_response`,
		},
		{
			CreatedAt: now.Add(-5 * time.Minute).Format(time.RFC3339), Endpoint: `/v1/images/generations`, Method: `POST`, Status: 200,
			ModelReturned: `gpt-image-2`, InputTokens: 300, OutputTokens: 300,
			CaptureMode: `usage_metered`, CaptureOutcome: `captured`, UsageSource: `http_response`,
			UsageDimensions: []UsageDimensionRecord{
				{Modality: `image`, Channel: `text`, Metric: `tokens`, Direction: `input`, Unit: `token`, Amount: 100},
				{Modality: `image`, Channel: `text`, Metric: `tokens`, Direction: `cached_input`, Unit: `token`, Amount: 40},
				{Modality: `image`, Channel: `image`, Metric: `tokens`, Direction: `input`, Unit: `token`, Amount: 200},
				{Modality: `image`, Channel: `image`, Metric: `tokens`, Direction: `cached_input`, Unit: `token`, Amount: 50},
				{Modality: `image`, Channel: `image`, Metric: `tokens`, Direction: `output`, Unit: `token`, Amount: 300},
			},
			ImageUsage: &ImageUsageRecord{Operation: `generation`, Size: `1024x1024`, InputImageCount: 2, ImageCount: 1},
		},
	}
	if err := d.InsertBatch(records); err != nil {
		t.Fatalf(`InsertBatch: %v`, err)
	}

	textRows, imageRows, err := d.CostBucketsContext(context.Background(), CostBucketFilter{Since: now.Add(-time.Hour)})
	if err != nil {
		t.Fatalf(`CostBucketsContext: %v`, err)
	}
	if len(textRows) != 3 {
		t.Fatalf(`text rows = %+v, want three homogeneous buckets`, textRows)
	}
	short := findTextCostBucket(t, textRows, `gemini-tier`, 150000, false)
	if short.RequestCount != 2 || short.InputTokens != 300000 || short.CachedTokens != 50000 || short.CacheCreationTokens != 20000 {
		t.Fatalf(`short bucket = %+v`, short)
	}
	if short.BillableUsageCount != 2 {
		t.Fatalf(`short billable usage count = %+v`, short)
	}
	if short.OutputTokens != 150 || short.ReasoningTokens != 20 || short.ObservedCount != 2 {
		t.Fatalf(`short output/confidence = %+v`, short)
	}
	long := findTextCostBucket(t, textRows, `gemini-tier`, 200000, false)
	if long.RequestCount != 1 || long.InputTokens != 200000 {
		t.Fatalf(`long bucket = %+v`, long)
	}
	imageText := findTextCostBucket(t, textRows, `gpt-image-2`, 300, true)
	if imageText.RequestCount != 1 {
		t.Fatalf(`image text boundary = %+v`, imageText)
	}

	if len(imageRows) != 1 {
		t.Fatalf(`image rows = %+v, want one`, imageRows)
	}
	image := imageRows[0]
	if image.Model != `gpt-image-2` || image.Operation != `generation` || image.Size != `1024x1024` {
		t.Fatalf(`image identity = %+v`, image)
	}
	if image.InputImageCount != 2 || image.OutputImageCount != 1 || image.MissingOutputCount != 0 {
		t.Fatalf(`image counts = %+v`, image)
	}
	if image.TokenUsageCount != 1 {
		t.Fatalf(`image token usage count = %+v`, image)
	}
	if image.InputTextTokens != 60 || image.CachedTextTokens != 40 || image.InputImageTokens != 150 || image.CachedImageTokens != 50 || image.OutputImageTokens != 300 {
		t.Fatalf(`image token normalization = %+v`, image)
	}
}

func TestCostBucketsGroupAndFilterKeys(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)
	key := `bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb`
	if err := d.InsertBatch([]UsageRecord{
		{CreatedAt: now.Format(time.RFC3339), Endpoint: `/v1/chat/completions`, Method: `POST`, Status: 200, APIKeyHash: key, ModelReturned: `model-a`, InputTokens: 10, CaptureOutcome: `captured`},
		{CreatedAt: now.Add(5 * time.Minute).Format(time.RFC3339), Endpoint: `/v1/chat/completions`, Method: `POST`, Status: 200, ModelReturned: `model-a`, InputTokens: 20, CaptureOutcome: `captured`},
	}); err != nil {
		t.Fatalf(`InsertBatch: %v`, err)
	}

	rows, _, err := d.CostBucketsContext(context.Background(), CostBucketFilter{
		Since: now.Add(-time.Hour), BucketSeconds: 3600, GroupByKey: true,
	})
	if err != nil {
		t.Fatalf(`grouped CostBucketsContext: %v`, err)
	}
	if len(rows) != 2 {
		t.Fatalf(`grouped rows = %+v, want two keys`, rows)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.KeyHash] = true
		if row.Bucket != now.Truncate(time.Hour).Format(time.RFC3339) {
			t.Fatalf(`bucket = %q`, row.Bucket)
		}
	}
	if !seen[key] || !seen[`unknown`] {
		t.Fatalf(`key groups = %+v`, seen)
	}

	unknownRows, _, err := d.CostBucketsContext(context.Background(), CostBucketFilter{Since: now.Add(-time.Hour), KeyHash: `unknown`})
	if err != nil {
		t.Fatalf(`unknown filter: %v`, err)
	}
	if len(unknownRows) != 1 || unknownRows[0].InputTokens != 20 {
		t.Fatalf(`unknown rows = %+v`, unknownRows)
	}
	exactRows, _, err := d.CostBucketsContext(context.Background(), CostBucketFilter{Since: now.Add(-time.Hour), KeyHash: key})
	if err != nil {
		t.Fatalf(`exact filter: %v`, err)
	}
	if len(exactRows) != 1 || exactRows[0].InputTokens != 10 {
		t.Fatalf(`exact rows = %+v`, exactRows)
	}
}

func findTextCostBucket(t *testing.T, rows []TextCostBucketRow, model string, requestInput int64, image bool) TextCostBucketRow {
	t.Helper()
	for _, row := range rows {
		if row.Model == model && row.RequestInputTokens == requestInput && row.ImageRequest == image {
			return row
		}
	}
	t.Fatalf(`missing text cost bucket model=%s request_input=%d image=%v in %+v`, model, requestInput, image, rows)
	return TextCostBucketRow{}
}

func assertModelSnapshotCostBucketsConserve(t *testing.T, snapshot *ModelsReportData) {
	t.Helper()
	requestCounts := make(map[string]int64)
	for _, row := range snapshot.TextCostBuckets {
		requestCounts[row.Model] += row.RequestCount
	}
	for _, model := range snapshot.Models {
		if requestCounts[model.Model] != model.RequestCount {
			t.Fatalf("cost bucket snapshot mismatch model=%s requests=%d cost_bucket_requests=%d", model.Model, model.RequestCount, requestCounts[model.Model])
		}
	}
}
