package report

import (
	"context"
	"errors"
	"testing"

	"ai-gateway-metering-proxy/internal/db"
)

func TestModelAssetsUsesUnifiedTieredCostAndConfidence(t *testing.T) {
	prices := mustParseCostPricing(t, `
pricing:
  tiered:
    input_per_1m: 2
    cached_input_per_1m: 1
    output_per_1m: 10
    reasoning_per_1m: 20
    cache_creation_per_1m: 3
    aliases: [tiered-alias]
    long_context:
      threshold_input_tokens: 200
      input_per_1m: 4
      cached_input_per_1m: 2
      output_per_1m: 18
      reasoning_per_1m: 30
      cache_creation_per_1m: 5
`)
	reader := &stubModelsReader{modelAssetsSnapshot: &db.ModelAssetsReportData{
		Rows: []db.ModelAssetRow{
			{
				Model: "tiered-alias", RequestCount: 2, FailedCount: 1,
				InputTokens: 300, OutputTokens: 30, ReasoningTokens: 5,
				CachedTokens: 30, CacheCreationTokens: 10, TotalTokens: 330,
				EndpointProfiles: "zeta,alpha", CaptureModes: "usage_metered",
				ModelSources: "requested,returned", LatestSeenAt: "2026-07-22T00:00:00Z",
			},
			{Model: "unpriced", RequestCount: 1, InputTokens: 10, TotalTokens: 10, CaptureModes: "usage_metered", ModelSources: "requested"},
			{Model: "request-only", RequestCount: 1, CaptureModes: "request_only", ModelSources: "requested"},
		},
		TextCostBuckets: []db.TextCostBucketRow{
			{
				Model: "tiered-alias", RequestInputTokens: 100, RequestCount: 1,
				InputTokens: 100, OutputTokens: 10, ReasoningTokens: 2, CachedTokens: 10, CacheCreationTokens: 4,
				ObservedCount: 1,
			},
			{
				Model: "tiered-alias", RequestInputTokens: 200, RequestCount: 1,
				InputTokens: 200, OutputTokens: 20, ReasoningTokens: 3, CachedTokens: 20, CacheCreationTokens: 6,
				ObservedCount: 1,
			},
			{Model: "unpriced", RequestInputTokens: 10, RequestCount: 1, InputTokens: 10, ObservedCount: 1},
			{Model: "request-only", RequestCount: 1, RequestOnlyCount: 1},
		},
	}}
	svc := NewService(testDependencies(reader), prices)

	got, err := svc.ModelAssets(context.Background(), ModelAssetsFilter{Range: "24h"})
	if err != nil {
		t.Fatalf("ModelAssets: %v", err)
	}
	if got.Range != "24h" || len(got.Items) != 3 {
		t.Fatalf("report = %+v", got)
	}
	byModel := make(map[string]ModelAssetItem)
	for _, item := range got.Items {
		byModel[item.Model] = item
	}
	tiered := byModel["tiered-alias"]
	wantShort := 86/1_000_000.0*2 + 10/1_000_000.0*1 + 8/1_000_000.0*10 + 2/1_000_000.0*20 + 4/1_000_000.0*3
	wantLong := 174/1_000_000.0*4 + 20/1_000_000.0*2 + 17/1_000_000.0*18 + 3/1_000_000.0*30 + 6/1_000_000.0*5
	assertCostNear(t, tiered.EstimatedCost, wantShort+wantLong)
	if !tiered.CostKnown || tiered.CostState != CostStateComplete || tiered.PricingSource != "exact" {
		t.Fatalf("tiered = %+v", tiered)
	}
	if len(tiered.EndpointProfiles) != 2 || tiered.EndpointProfiles[0] != "alpha" || tiered.EndpointProfiles[1] != "zeta" {
		t.Fatalf("profiles = %+v", tiered.EndpointProfiles)
	}
	if tiered.ReasoningTokens != 5 || tiered.CachedTokens != 30 || tiered.CacheCreationTokens != 10 {
		t.Fatalf("token details = %+v", tiered)
	}
	unpriced := byModel["unpriced"]
	if unpriced.CostKnown || unpriced.CostState != CostStatePartial || unpriced.PricingSource != "unpriced" {
		t.Fatalf("unpriced = %+v", unpriced)
	}
	requestOnly := byModel["request-only"]
	if !requestOnly.CostKnown || requestOnly.CostState != CostStatePartial || requestOnly.Confidence != "request_only" {
		t.Fatalf("request-only = %+v", requestOnly)
	}
	if got.Summary.UnpricedUsedModels != 2 || got.Summary.RequestOnlyModels != 1 || !got.Summary.CostPartial {
		t.Fatalf("summary = %+v", got.Summary)
	}
}

func TestModelAssetsPropagatesSnapshotError(t *testing.T) {
	want := errors.New("model assets failed")
	reader := &stubModelsReader{modelAssetsErr: want}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	if _, err := svc.ModelAssets(context.Background(), ModelAssetsFilter{}); !errors.Is(err, want) {
		t.Fatalf("ModelAssets err=%v, want %v", err, want)
	}
}
