package report

import (
	"context"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

type conservationCapture struct{}

func (conservationCapture) Snapshot() (queueDepth, dropped, parseErrors, dbErrors int64) {
	return 0, 0, 0, 0
}

func TestCostConservationAcrossProductionReportBoundaries(t *testing.T) {
	database, err := db.Open(t.TempDir() + "/conservation.sqlite")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})

	prices := mustParseCostPricing(t, `
pricing:
  tiered:
    input_per_1m: 2
    cached_input_per_1m: 1
    cache_creation_per_1m: 3
    output_per_1m: 10
    reasoning_per_1m: 30
    long_context:
      threshold_input_tokens: 200000
      input_per_1m: 4
      cached_input_per_1m: 2
      cache_creation_per_1m: 6
      output_per_1m: 20
      reasoning_per_1m: 40
  known:
    input_per_1m: 1
    output_per_1m: 5
multimodal_pricing:
  per-image:
    image:
      per_image_input: 0.01
      per_image_output:
        default: 0.05
        "1K": 0.05
`)

	now := time.Now().UTC().Truncate(time.Second)
	since := now.Add(-4 * time.Hour)
	keyA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	keyB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	keyPartial := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	base := func(at time.Time, key, model string) db.UsageRecord {
		return db.UsageRecord{
			CreatedAt:           at.Format(time.RFC3339),
			RequestID:           "conservation-" + at.Format("150405"),
			Endpoint:            "/v1/responses",
			Method:              "POST",
			Status:              200,
			LatencyMs:           100,
			TTFBMs:              25,
			APIKeyHash:          key,
			ModelRequested:      model,
			ModelReturned:       model,
			EndpointProfile:     "responses",
			CaptureMode:         "usage_metered",
			MeteringKind:        "llm_tokens",
			CaptureOutcome:      "captured",
			ModelReturnedSource: "response_body",
			UsageSource:         "http_response",
		}
	}

	short := base(now.Add(-3*time.Hour), keyA, "tiered")
	short.InputTokens = 150000
	short.CachedTokens = 20000
	short.CacheCreationTokens = 10000
	short.OutputTokens = 100
	short.ReasoningTokens = 20
	short.TotalTokens = 150100

	long := base(now.Add(-90*time.Minute), keyA, "tiered")
	long.InputTokens = 200000
	long.CachedTokens = 50000
	long.OutputTokens = 100
	long.ReasoningTokens = 10
	long.TotalTokens = 200100

	known := base(now.Add(-45*time.Minute), keyB, "known")
	known.InputTokens = 1000
	known.OutputTokens = 500
	known.TotalTokens = 1500

	image := base(now.Add(-15*time.Minute), "", "per-image")
	image.Endpoint = "/v1/images/generations"
	image.EndpointProfile = "openai_images_generations"
	image.MeteringKind = "image_tokens"
	image.ImageUsage = &db.ImageUsageRecord{
		Operation:       "generation",
		Size:            "1024x1024",
		InputImageCount: 1,
		ImageCount:      2,
	}

	unpriced := base(now.Add(-5*time.Minute), keyPartial, "unpriced")
	unpriced.InputTokens = 100
	unpriced.TotalTokens = 100

	if err := database.InsertBatch([]db.UsageRecord{short, long, known, image, unpriced}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	deps := Dependencies{
		Models: database, Summary: database, Timeseries: database, Images: database,
		Overview: database, Capture: conservationCapture{}, ModelAssets: database,
		Keys: database, Activity: database, Requests: database, Issues: database,
		Multimodal: database, ImageRequests: database, Errors: database, Gateway: database,
		KeyLabels: map[string]string{keyA: "alpha", keyB: "beta"},
	}
	svc := NewService(deps, prices)
	ctx := context.Background()

	summary, err := svc.Summary(ctx, SummaryFilter{Since: since})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	models, err := svc.Models(ctx, ModelsFilter{Since: since})
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	series, err := svc.Timeseries(ctx, TimeseriesFilter{Since: since, BucketMin: 60})
	if err != nil {
		t.Fatalf("Timeseries: %v", err)
	}
	keys, err := svc.Keys(ctx, KeysFilter{Since: since})
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	overview, err := svc.Overview(ctx, OverviewFilter{Since: since, Range: "24h"})
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	assets, err := svc.ModelAssets(ctx, ModelAssetsFilter{Since: since, Range: "24h"})
	if err != nil {
		t.Fatalf("ModelAssets: %v", err)
	}

	const expectedKnownSubtotal = 1.1071
	assertCostNear(t, summary.TotalCost, expectedKnownSubtotal)
	assertCostNear(t, sumModelCosts(models), summary.TotalCost)
	assertCostNear(t, sumTimeseriesCosts(series), summary.TotalCost)
	assertCostNear(t, sumKeyCosts(keys), summary.TotalCost)
	assertCostNear(t, overview.Selected.Data.TotalCost, summary.TotalCost)
	assertCostNear(t, overview.Cost.Data.KnownCost, summary.TotalCost)
	assertCostNear(t, sumModelAssetCosts(assets.Items), summary.TotalCost)

	if summary.TotalRequests != 5 {
		t.Fatalf("summary requests = %d, want 5", summary.TotalRequests)
	}
	if got := sumModelRequests(models); got != summary.TotalRequests {
		t.Fatalf("model request conservation = %d, want %d", got, summary.TotalRequests)
	}
	if got := sumTimeseriesRequests(series); got != summary.TotalRequests {
		t.Fatalf("timeseries request conservation = %d, want %d", got, summary.TotalRequests)
	}
	if got := sumKeyRequests(keys); got != summary.TotalRequests {
		t.Fatalf("key request conservation = %d, want %d", got, summary.TotalRequests)
	}
	if summary.CostState != CostStatePartial || summary.CostKnown || summary.UnpricedModels != 1 {
		t.Fatalf("summary cost completeness = %+v", summary)
	}
	if overview.Cost.Data.CostState != CostStatePartial || overview.Cost.Data.CostKnown {
		t.Fatalf("overview cost completeness = %+v", overview.Cost)
	}
	if len(keys) != 4 {
		t.Fatalf("keys = %d, want alpha, beta, partial, and unknown", len(keys))
	}
}

func sumModelCosts(rows []ModelReport) (sum float64) {
	for _, row := range rows {
		sum += row.Cost
	}
	return sum
}

func sumTimeseriesCosts(rows []TimeseriesReport) (sum float64) {
	for _, row := range rows {
		sum += row.Cost
	}
	return sum
}

func sumKeyCosts(rows []KeyReport) (sum float64) {
	for _, row := range rows {
		sum += row.EstimatedCost
	}
	return sum
}

func sumModelAssetCosts(rows []ModelAssetItem) (sum float64) {
	for _, row := range rows {
		sum += row.EstimatedCost
	}
	return sum
}

func sumModelRequests(rows []ModelReport) (sum int64) {
	for _, row := range rows {
		sum += row.RequestCount
	}
	return sum
}

func sumTimeseriesRequests(rows []TimeseriesReport) (sum int64) {
	for _, row := range rows {
		sum += row.Count
	}
	return sum
}

func sumKeyRequests(rows []KeyReport) (sum int64) {
	for _, row := range rows {
		sum += row.RequestCount
	}
	return sum
}
