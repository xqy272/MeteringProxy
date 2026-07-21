package report

import (
	"testing"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/pricing"
)

func TestEvaluateCostBucketsTierAndImageDoubleCountBoundary(t *testing.T) {
	prices := mustParseCostPricing(t, `
pricing:
  tiered:
    input_per_1m: 2
    output_per_1m: 10
    long_context:
      threshold_input_tokens: 200000
      input_per_1m: 4
      output_per_1m: 20
multimodal_pricing:
  token-image:
    text:
      input_per_1m: 5
    image:
      input_per_1m: 8
      output_per_1m: 30
`)
	textRows := []db.TextCostBucketRow{
		{Model: "tiered", RequestInputTokens: 150000, RequestCount: 2, InputTokens: 300000, OutputTokens: 200, ObservedCount: 2},
		{Model: "tiered", RequestInputTokens: 200000, RequestCount: 1, InputTokens: 200000, OutputTokens: 100, ObservedCount: 1},
		// These summary tokens must be skipped because this request resolves to
		// multimodal pricing; only the channel dimensions below are billed.
		{Model: "token-image", RequestInputTokens: 999999, ImageRequest: true, RequestCount: 1, InputTokens: 999999, OutputTokens: 999999, ObservedCount: 1},
	}
	imageRows := []db.ImageCostBucketRow{{
		Model: "token-image", Operation: "generation", Size: "1024x1024", RequestCount: 1,
		OutputImageCount: 1, InputTextTokens: 100, InputImageTokens: 200, OutputImageTokens: 300,
	}}

	results := evaluateCostBuckets(prices, textRows, imageRows, costGroupByModel)
	tiered := results[CostGroup{Model: "tiered"}]
	wantTiered := 300000/1_000_000.0*2 + 200/1_000_000.0*10 + 200000/1_000_000.0*4 + 100/1_000_000.0*20
	assertCostNear(t, tiered.Amount, wantTiered)
	if !tiered.CostKnown || tiered.State != CostStateComplete || tiered.UsageConfidenceCounts.Observed != 3 {
		t.Fatalf("tiered result = %+v", tiered)
	}

	image := results[CostGroup{Model: "token-image"}]
	wantImage := 100/1_000_000.0*5 + 200/1_000_000.0*8 + 300/1_000_000.0*30
	assertCostNear(t, image.Amount, wantImage)
	if !image.CostKnown || image.State != CostStateComplete {
		t.Fatalf("token-only image result = %+v", image)
	}
	// If request_usage summary had also been billed, this would be orders of
	// magnitude larger than the channel-only result.
	if image.Amount > 0.1 {
		t.Fatalf("image cost appears double-counted: %+v", image)
	}
}

func TestEvaluateCostBucketsPerImagePartialReasonsStable(t *testing.T) {
	prices := mustParseCostPricing(t, `
multimodal_pricing:
  per-image:
    image:
      per_image_input: 0.01
      per_image_output:
        default: 0.05
        "1K": 0.05
        "2K": 0.07
`)
	textRows := []db.TextCostBucketRow{{
		Model: "per-image", ImageRequest: true, RequestCount: 4,
		RequestOnlyCount: 1, MissingUsageCount: 1, UnsupportedCount: 1, ConflictCount: 1,
	}}
	imageRows := []db.ImageCostBucketRow{{
		Model: "per-image", Operation: "generation", Size: "unknown-size",
		RequestCount: 4, InputImageCount: 2, OutputImageCount: 1, MissingOutputCount: 1,
	}}

	result := evaluateCostBuckets(prices, textRows, imageRows, costGroupByModel)[CostGroup{Model: "per-image"}]
	assertCostNear(t, result.Amount, 0.07)
	if !result.CostKnown || result.State != CostStatePartial {
		t.Fatalf("result = %+v", result)
	}
	want := []PartialReason{
		PartialReasonMissingUsage,
		PartialReasonRequestOnly,
		PartialReasonUnsupported,
		PartialReasonUsageConflict,
		PartialReasonImageCountMissing,
		PartialReasonImageSizeDefaulted,
	}
	if len(result.PartialReasons) != len(want) {
		t.Fatalf("reasons = %+v, want %+v", result.PartialReasons, want)
	}
	for i := range want {
		if result.PartialReasons[i] != want[i] {
			t.Fatalf("reasons = %+v, want %+v", result.PartialReasons, want)
		}
	}
}

func TestEvaluateCostBucketsKnownSubtotalForMissingPrice(t *testing.T) {
	prices := mustParseCostPricing(t, `
pricing:
  known:
    input_per_1m: 1
    output_per_1m: 2
multimodal_pricing:
  partial-image:
    image:
      per_image_input: 0.01
`)
	textRows := []db.TextCostBucketRow{
		{Model: "known", RequestInputTokens: 1000, RequestCount: 1, InputTokens: 1000, ObservedCount: 1},
		{Model: "unknown", RequestInputTokens: 500, RequestCount: 1, InputTokens: 500, ObservedCount: 1},
		{Model: "partial-image", ImageRequest: true, RequestCount: 1, ObservedCount: 1},
	}
	imageRows := []db.ImageCostBucketRow{{
		Model: "partial-image", Size: "1K", InputImageCount: 2, OutputImageCount: 1,
	}}
	results := evaluateCostBuckets(prices, textRows, imageRows, costGroupByModel)

	known := results[CostGroup{Model: "known"}]
	assertCostNear(t, known.Amount, 0.001)
	if !known.CostKnown || known.State != CostStateComplete {
		t.Fatalf("known = %+v", known)
	}

	unknown := results[CostGroup{Model: "unknown"}]
	if unknown.CostKnown || unknown.State != CostStatePartial || unknown.UnpricedModels != 1 || unknown.PartialReasons[0] != PartialReasonUnpricedModel {
		t.Fatalf("unknown = %+v", unknown)
	}

	partialImage := results[CostGroup{Model: "partial-image"}]
	assertCostNear(t, partialImage.Amount, 0.02)
	if partialImage.CostKnown || partialImage.State != CostStatePartial || partialImage.UnpricedModels != 1 {
		t.Fatalf("partial image = %+v", partialImage)
	}

	global := evaluateCostBuckets(prices, textRows, imageRows, 0)[CostGroup{}]
	assertCostNear(t, global.Amount, 0.021)
	if global.CostKnown || global.UnpricedModels != 2 || global.State != CostStatePartial {
		t.Fatalf("global result = %+v", global)
	}
}

func TestEvaluateCostBucketsTokenPricedImageWithoutDimensionsIsPartial(t *testing.T) {
	prices := mustParseCostPricing(t, `
multimodal_pricing:
  token-image:
    image:
      input_per_1m: 8
      output_per_1m: 30
`)
	results := evaluateCostBuckets(prices,
		[]db.TextCostBucketRow{{Model: "token-image", ImageRequest: true, RequestCount: 1, ObservedCount: 1}},
		[]db.ImageCostBucketRow{{Model: "token-image", RequestCount: 1, OutputImageCount: 1, ObservedCount: 1}},
		costGroupByModel,
	)
	result := results[CostGroup{Model: "token-image"}]
	if !result.CostKnown || result.State != CostStatePartial || len(result.PartialReasons) != 1 || result.PartialReasons[0] != PartialReasonMissingUsage {
		t.Fatalf("result = %+v", result)
	}
}

func TestEvaluateCostBucketsTextFallbackImageTracksMissingRequestUsage(t *testing.T) {
	prices := mustParseCostPricing(t, `
pricing:
  blended-image:
    input_per_1m: 1
    output_per_1m: 3
`)
	results := evaluateCostBuckets(prices,
		[]db.TextCostBucketRow{{
			Model: "blended-image", ImageRequest: true, RequestCount: 2,
			BillableUsageCount: 1, InputTokens: 100, OutputTokens: 50,
			ObservedCount: 2,
		}},
		[]db.ImageCostBucketRow{{Model: "blended-image", RequestCount: 2, OutputImageCount: 2}},
		costGroupByModel,
	)
	result := results[CostGroup{Model: "blended-image"}]
	want := 100/1_000_000.0 + 50/1_000_000.0*3
	assertCostNear(t, result.Amount, want)
	if !result.CostKnown || result.State != CostStatePartial || len(result.PartialReasons) != 1 || result.PartialReasons[0] != PartialReasonMissingUsage {
		t.Fatalf("result = %+v", result)
	}
}

func mustParseCostPricing(t *testing.T, body string) *pricing.Pricing {
	t.Helper()
	p, err := pricing.Parse([]byte(body))
	if err != nil {
		t.Fatalf("pricing.Parse: %v", err)
	}
	return p
}

func assertCostNear(t *testing.T, got, want float64) {
	t.Helper()
	diff := got - want
	if diff < -0.000000001 || diff > 0.000000001 {
		t.Fatalf("cost = %.12f, want %.12f", got, want)
	}
}
