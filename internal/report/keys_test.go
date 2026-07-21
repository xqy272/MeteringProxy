package report

import (
	"context"
	"errors"
	"testing"

	"ai-gateway-metering-proxy/internal/db"
)

func TestKeysAddsLabelsCompletenessAndConservesCost(t *testing.T) {
	keyA := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	keyB := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	prices := mustParseCostPricing(t, `
pricing:
  known:
    input_per_1m: 1
    output_per_1m: 2
multimodal_pricing:
  per-image:
    image:
      per_image_input: 0.01
      per_image_output:
        default: 0.05
        "1K": 0.05
`)
	snapshot := &db.KeysReportData{
		Rows: []db.KeyRow{
			{KeyHash: keyA, RequestCount: 2, FailedCount: 1, ModelCount: 2, InputTokens: 100, OutputTokens: 50, TotalTokens: 150, AvgLatencyMs: 200, AvgTTFBMs: 50, LatestSeenAt: "2026-07-22T00:00:00Z"},
			{KeyHash: keyB, RequestCount: 1, ModelCount: 1, InputTokens: 10, TotalTokens: 10},
			{KeyHash: "unknown", RequestCount: 1, ModelCount: 1},
		},
		TextCostBuckets: []db.TextCostBucketRow{
			{KeyHash: keyA, Model: "known", RequestInputTokens: 100, RequestCount: 1, InputTokens: 100, OutputTokens: 50, ObservedCount: 1},
			{KeyHash: keyA, Model: "request-only", RequestCount: 1, RequestOnlyCount: 1},
			{KeyHash: keyB, Model: "unpriced", RequestInputTokens: 10, RequestCount: 1, InputTokens: 10, MissingUsageCount: 1},
			{KeyHash: "unknown", Model: "per-image", ImageRequest: true, RequestCount: 1, ObservedCount: 1},
		},
		ImageCostBuckets: []db.ImageCostBucketRow{
			{KeyHash: "unknown", Model: "per-image", Size: "1024x1024", RequestCount: 1, InputImageCount: 2, OutputImageCount: 1, ObservedCount: 1},
		},
	}
	reader := &stubModelsReader{keysSnapshot: snapshot}
	deps := testDependencies(reader)
	deps.KeyLabels = map[string]string{keyA: "friend-a"}
	svc := NewService(deps, prices)

	got, err := svc.Keys(context.Background(), KeysFilter{})
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("keys = %+v", got)
	}
	byKey := make(map[string]KeyReport)
	var sum float64
	for _, item := range got {
		byKey[item.KeyHash] = item
		sum += item.EstimatedCost
	}
	a := byKey[keyA]
	if a.Label != "friend-a" || a.FailureRate != 0.5 || a.ModelCount != 2 || a.AvgLatencyMs != 200 || a.LatestSeenAt == "" {
		t.Fatalf("key A = %+v", a)
	}
	if !a.CostKnown || a.CostState != CostStatePartial || len(a.PartialReasons) != 1 || a.PartialReasons[0] != PartialReasonRequestOnly {
		t.Fatalf("key A cost = %+v", a)
	}
	b := byKey[keyB]
	if b.Label != "" || b.CostKnown || b.UnpricedModels != 1 || b.MissingUsageCount != 1 {
		t.Fatalf("key B = %+v", b)
	}
	unknown := byKey["unknown"]
	assertCostNear(t, unknown.EstimatedCost, 0.07)
	if unknown.Label != "" || !unknown.CostKnown || unknown.CostState != CostStateComplete {
		t.Fatalf("unknown = %+v", unknown)
	}
	global := evaluateCostBuckets(prices, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, 0)[CostGroup{}]
	assertCostNear(t, sum, global.Amount)
}

func TestKeysPropagatesSnapshotErrorAndReturnsNonNilEmpty(t *testing.T) {
	want := errors.New("keys failed")
	reader := &stubModelsReader{keysErr: want}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	if rows, err := svc.Keys(context.Background(), KeysFilter{}); !errors.Is(err, want) || rows != nil {
		t.Fatalf("Keys rows=%#v err=%v", rows, err)
	}
	reader.keysErr = nil
	rows, err := svc.Keys(context.Background(), KeysFilter{})
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty Keys rows=%#v err=%v", rows, err)
	}
}
