package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

func TestOverviewBuildsTypedSectionsAndUnifiedCost(t *testing.T) {
	prices := mustParseCostPricing(t, `
pricing:
  known:
    input_per_1m: 1
    output_per_1m: 2
`)
	now := time.Date(2026, 7, 22, 6, 0, 0, 0, time.UTC)
	reader := &stubModelsReader{
		overviewSnapshot: &db.OverviewReportData{
			Selected: db.OverviewSelectedRow{
				TotalRequests: 2, FailedRequests: 1, TotalInputTokens: 110,
				TotalOutputTokens: 50, TotalTokens: 160, P95LatencyMs: 300, P95TTFBMs: 80,
			},
			Recent: db.OverviewRecentRow{
				TotalRequests: 1, FailedRequests: 1, P95LatencyMs: 300,
				LatestError: &db.OverviewLatestErrorRow{RequestID: "req-failed", Status: 500, Class: "upstream_5xx"},
			},
			CaptureFailed: 1,
			TextCostBuckets: []db.TextCostBucketRow{
				{Model: "known", RequestInputTokens: 100, RequestCount: 1, InputTokens: 100, OutputTokens: 50, ObservedCount: 1},
				{Model: "unknown", RequestInputTokens: 10, RequestCount: 1, InputTokens: 10, MissingUsageCount: 1},
			},
		},
		runtimeQueueDepth: 2,
		runtimeDropped:    3,
	}
	svc := NewService(testDependencies(reader), prices)
	svc.now = func() time.Time { return now }
	since := now.Add(-24 * time.Hour)

	got, err := svc.Overview(context.Background(), OverviewFilter{Since: since, Range: "24h"})
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if got.Range != "24h" || got.Selected.Data.TotalRequests != 2 || got.Selected.Data.FailureRate != 0.5 {
		t.Fatalf("selected = %+v", got.Selected)
	}
	wantCost := 100/1_000_000.0 + 50/1_000_000.0*2
	assertCostNear(t, got.Selected.Data.TotalCost, wantCost)
	if got.Selected.Status != SectionStatusPartial || got.Cost.Status != SectionStatusPartial {
		t.Fatalf("section status selected=%s cost=%s", got.Selected.Status, got.Cost.Status)
	}
	if got.Cost.Data.CostKnown || got.Cost.Data.UnpricedModels != 1 || !got.Cost.Data.Partial {
		t.Fatalf("cost = %+v", got.Cost.Data)
	}
	if got.Recent1h.Data.LatestError == nil || got.Recent1h.Data.LatestError.RequestID != "req-failed" {
		t.Fatalf("recent = %+v", got.Recent1h)
	}
	if got.Capture.Data.Status != "attention" || got.Capture.Data.QueueDepth != 2 || got.Capture.Data.DroppedEvents != 3 || got.Capture.Data.CaptureFailed != 1 {
		t.Fatalf("capture = %+v", got.Capture)
	}
	if reader.overviewCalls != 1 || !reader.overviewSince.Equal(since) || !reader.overviewRecent.Equal(now.Add(-time.Hour)) {
		t.Fatalf("reader calls=%d since=%v recent=%v", reader.overviewCalls, reader.overviewSince, reader.overviewRecent)
	}
}

func TestOverviewPropagatesSnapshotError(t *testing.T) {
	want := errors.New("overview snapshot failed")
	reader := &stubModelsReader{overviewErr: want}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	if _, err := svc.Overview(context.Background(), OverviewFilter{}); !errors.Is(err, want) {
		t.Fatalf("Overview err=%v, want %v", err, want)
	}
}
