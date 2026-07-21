package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

func TestSummaryUsesUnifiedKnownSubtotal(t *testing.T) {
	reader := &stubModelsReader{summarySnapshot: &db.SummaryReportData{
		Summary: db.SummaryRow{TotalRequests: 2, FailedRequests: 1, TotalInputTokens: 1500, TotalOutputTokens: 300, TotalTokens: 1800},
		TextCostBuckets: []db.TextCostBucketRow{
			{Model: "known", RequestInputTokens: 1000, RequestCount: 1, BillableUsageCount: 1, InputTokens: 1000, OutputTokens: 200, ObservedCount: 1},
			{Model: "unknown", RequestInputTokens: 500, RequestCount: 1, BillableUsageCount: 1, InputTokens: 500, OutputTokens: 100, MissingUsageCount: 1},
		},
	}}
	prices := mustParseCostPricing(t, `
pricing:
  known:
    input_per_1m: 1
    output_per_1m: 2
`)
	svc := NewService(testDependencies(reader), prices)

	got, err := svc.Summary(context.Background(), SummaryFilter{Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	assertCostNear(t, got.TotalCost, 0.0014)
	if got.TotalRequests != 2 || got.FailedRequests != 1 || got.CostKnown || got.CostState != CostStatePartial || got.UnpricedModels != 1 {
		t.Fatalf("summary = %+v", got)
	}
	wantReasons := []PartialReason{PartialReasonUnpricedModel, PartialReasonMissingUsage}
	if len(got.PartialReasons) != len(wantReasons) {
		t.Fatalf("reasons = %+v", got.PartialReasons)
	}
	for i := range wantReasons {
		if got.PartialReasons[i] != wantReasons[i] {
			t.Fatalf("reasons = %+v", got.PartialReasons)
		}
	}
}

func TestTimeseriesUsesBucketedUnifiedCost(t *testing.T) {
	bucketA := "2026-07-22T01:00:00Z"
	bucketB := "2026-07-22T02:00:00Z"
	reader := &stubModelsReader{timeseriesSnapshot: &db.TimeseriesReportData{
		Rows: []db.TimeseriesRow{
			{Timestamp: bucketA, Count: 2, InputTokens: 300000, TotalTokens: 300000},
			{Timestamp: bucketB, Count: 1, InputTokens: 200000, TotalTokens: 200000},
		},
		TextCostBuckets: []db.TextCostBucketRow{
			{Bucket: bucketA, Model: "tiered", RequestInputTokens: 150000, RequestCount: 2, BillableUsageCount: 2, InputTokens: 300000, ObservedCount: 2},
			{Bucket: bucketB, Model: "tiered", RequestInputTokens: 200000, RequestCount: 1, BillableUsageCount: 1, InputTokens: 200000, ObservedCount: 1},
		},
	}}
	prices := mustParseCostPricing(t, `
pricing:
  tiered:
    input_per_1m: 2
    output_per_1m: 10
    long_context:
      threshold_input_tokens: 200000
      input_per_1m: 4
      output_per_1m: 20
`)
	svc := NewService(testDependencies(reader), prices)
	got, err := svc.Timeseries(context.Background(), TimeseriesFilter{Since: time.Now().Add(-time.Hour), BucketMin: 60})
	if err != nil {
		t.Fatalf("Timeseries: %v", err)
	}
	if len(got) != 2 || reader.lastBucketMin != 60 {
		t.Fatalf("timeseries = %+v bucketMin=%d", got, reader.lastBucketMin)
	}
	assertCostNear(t, got[0].Cost, 0.6)
	assertCostNear(t, got[1].Cost, 0.8)
	if !got[0].CostKnown || got[0].CostState != CostStateComplete || !got[1].CostKnown || got[1].CostState != CostStateComplete {
		t.Fatalf("timeseries states = %+v", got)
	}
}

func TestSummaryAndTimeseriesReaderErrorsPropagate(t *testing.T) {
	want := errors.New("read failed")
	reader := &stubModelsReader{summaryErr: want, timeseriesErr: want}
	svc := NewService(testDependencies(reader), mustParseCostPricing(t, ""))
	if _, err := svc.Summary(context.Background(), SummaryFilter{}); !errors.Is(err, want) {
		t.Fatalf("Summary err=%v", err)
	}
	if _, err := svc.Timeseries(context.Background(), TimeseriesFilter{}); !errors.Is(err, want) {
		t.Fatalf("Timeseries err=%v", err)
	}
}
