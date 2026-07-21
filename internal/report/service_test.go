package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

type stubModelsReader struct {
	rows      []db.ModelRow
	returned  map[string]map[string]int64
	usage     map[string]map[string]int64
	err       error
	calls     int
	lastSince time.Time
	lastDone  bool
}

func (s *stubModelsReader) ModelsReportSnapshot(ctx context.Context, since time.Time) ([]db.ModelRow, map[string]map[string]int64, map[string]map[string]int64, error) {
	s.calls++
	s.lastSince = since
	s.lastDone = ctx.Err() != nil
	if s.err != nil {
		return nil, nil, nil, s.err
	}
	return s.rows, s.returned, s.usage, nil
}

type stubCostEngine struct {
	cost  float64
	known bool
	calls int
	model string
}

func (s *stubCostEngine) CostWithCacheCreation(model string, inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheCreationTokens int64) (float64, bool) {
	s.calls++
	s.model = model
	_, _, _, _, _ = inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheCreationTokens
	return s.cost, s.known
}

func TestModelsMapsAndMergesSources(t *testing.T) {
	reader := &stubModelsReader{
		rows: []db.ModelRow{{
			Model:               "gpt-4o",
			ModelSource:         "returned",
			RequestCount:        2,
			FailedCount:         1,
			InputTokens:         100,
			OutputTokens:        50,
			ReasoningTokens:     5,
			CachedTokens:        10,
			CacheCreationTokens: 3,
			TotalTokens:         155,
			MissingUsageCount:   1,
		}},
		returned: map[string]map[string]int64{
			"gpt-4o": {"response_body": 2},
		},
		usage: map[string]map[string]int64{
			"gpt-4o": {"http_response": 1, "none": 1},
		},
	}
	cost := &stubCostEngine{cost: 1.25, known: true}
	svc := NewService(reader, cost)

	since := time.Now().Add(-time.Hour)
	got, err := svc.Models(context.Background(), ModelsFilter{Since: since})
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	item := got[0]
	if item.Model != "gpt-4o" || item.RequestCount != 2 || item.MissingUsageCount != 1 {
		t.Fatalf("item = %+v", item)
	}
	if item.Cost != 1.25 || !item.CostKnown {
		t.Fatalf("cost = %.2f known=%v", item.Cost, item.CostKnown)
	}
	if item.ModelReturnedSourceCounts["response_body"] != 2 {
		t.Fatalf("returned sources = %+v", item.ModelReturnedSourceCounts)
	}
	if item.UsageSourceCounts["http_response"] != 1 || item.UsageSourceCounts["none"] != 1 {
		t.Fatalf("usage sources = %+v", item.UsageSourceCounts)
	}
	if reader.calls != 1 {
		t.Fatalf("snapshot calls = %d, want 1", reader.calls)
	}
	if !reader.lastSince.Equal(since) {
		t.Fatalf("filter not forwarded: %v", reader.lastSince)
	}
	if cost.calls != 1 || cost.model != "gpt-4o" {
		t.Fatalf("cost calls=%d model=%q", cost.calls, cost.model)
	}
}

func TestModelsCostKnownForZeroTokenUnpriced(t *testing.T) {
	reader := &stubModelsReader{
		rows: []db.ModelRow{{
			Model:        "unknown-model",
			ModelSource:  "requested",
			RequestCount: 1,
		}},
	}
	cost := &stubCostEngine{cost: 0, known: false}
	svc := NewService(reader, cost)

	got, err := svc.Models(context.Background(), ModelsFilter{Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(got) != 1 || !got[0].CostKnown || got[0].Cost != 0 {
		t.Fatalf("got = %+v, want known zero cost", got)
	}
}

func TestModelsSnapshotErrorIsAtomic(t *testing.T) {
	want := errors.New("snapshot query failed")
	reader := &stubModelsReader{err: want}
	svc := NewService(reader, &stubCostEngine{})

	got, err := svc.Models(context.Background(), ModelsFilter{Since: time.Now()})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if got != nil {
		t.Fatalf("got = %#v, want nil result on atomic failure", got)
	}
	if reader.calls != 1 {
		t.Fatalf("calls = %d", reader.calls)
	}
}

func TestModelsEmptyResult(t *testing.T) {
	reader := &stubModelsReader{}
	svc := NewService(reader, &stubCostEngine{})
	got, err := svc.Models(context.Background(), ModelsFilter{Since: time.Now()})
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("got = %#v, want empty non-nil slice", got)
	}
}

func TestModelsUsesRequestContext(t *testing.T) {
	reader := &stubModelsReader{
		rows: []db.ModelRow{{Model: "gpt-4o"}},
	}
	svc := NewService(reader, &stubCostEngine{known: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.Models(ctx, ModelsFilter{Since: time.Now()})
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if !reader.lastDone {
		t.Fatal("expected canceled context to reach reader")
	}
}

func TestNewServiceRequiresDependencies(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil ModelsReader")
		}
	}()
	_ = NewService(nil, &stubCostEngine{})
}
