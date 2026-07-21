package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/pricing"
)

type stubModelsReader struct {
	snapshot            *db.ModelsReportData
	err                 error
	summarySnapshot     *db.SummaryReportData
	summaryErr          error
	timeseriesSnapshot  *db.TimeseriesReportData
	timeseriesErr       error
	imageSnapshot       *db.ImageReportData
	imageErr            error
	imageCalls          int
	imageSince          time.Time
	imageDone           bool
	overviewSnapshot    *db.OverviewReportData
	overviewErr         error
	overviewCalls       int
	overviewSince       time.Time
	overviewRecent      time.Time
	runtimeQueueDepth   int64
	runtimeDropped      int64
	runtimeParseErrors  int64
	runtimeDBErrors     int64
	modelAssetsSnapshot *db.ModelAssetsReportData
	modelAssetsErr      error
	keysSnapshot        *db.KeysReportData
	keysErr             error
	lastBucketMin       int
	calls               int
	lastSince           time.Time
	lastDone            bool
}

func (s *stubModelsReader) ModelsReportSnapshot(ctx context.Context, since time.Time) (*db.ModelsReportData, error) {
	s.calls++
	s.lastSince = since
	s.lastDone = ctx.Err() != nil
	if s.err != nil {
		return nil, s.err
	}
	if s.snapshot == nil {
		return &db.ModelsReportData{}, nil
	}
	return s.snapshot, nil
}

func (s *stubModelsReader) SummaryReportSnapshot(ctx context.Context, since time.Time) (*db.SummaryReportData, error) {
	if s.summaryErr != nil {
		return nil, s.summaryErr
	}
	if s.summarySnapshot == nil {
		return &db.SummaryReportData{}, nil
	}
	return s.summarySnapshot, nil
}

func (s *stubModelsReader) TimeseriesReportSnapshot(ctx context.Context, since time.Time, bucketMin int) (*db.TimeseriesReportData, error) {
	s.lastBucketMin = bucketMin
	if s.timeseriesErr != nil {
		return nil, s.timeseriesErr
	}
	if s.timeseriesSnapshot == nil {
		return &db.TimeseriesReportData{}, nil
	}
	return s.timeseriesSnapshot, nil
}

func (s *stubModelsReader) ImageReportSnapshot(ctx context.Context, since time.Time) (*db.ImageReportData, error) {
	s.imageCalls++
	s.imageSince = since
	s.imageDone = ctx.Err() != nil
	if s.imageErr != nil {
		return nil, s.imageErr
	}
	if s.imageSnapshot == nil {
		return &db.ImageReportData{}, nil
	}
	return s.imageSnapshot, nil
}

func (s *stubModelsReader) OverviewReportSnapshot(ctx context.Context, since, recentSince time.Time) (*db.OverviewReportData, error) {
	s.overviewCalls++
	s.overviewSince = since
	s.overviewRecent = recentSince
	if s.overviewErr != nil {
		return nil, s.overviewErr
	}
	if s.overviewSnapshot == nil {
		return &db.OverviewReportData{}, nil
	}
	return s.overviewSnapshot, nil
}

func (s *stubModelsReader) Snapshot() (queueDepth, dropped, parseErrors, dbErrors int64) {
	return s.runtimeQueueDepth, s.runtimeDropped, s.runtimeParseErrors, s.runtimeDBErrors
}

func (s *stubModelsReader) ModelAssetsReportSnapshot(ctx context.Context, since time.Time) (*db.ModelAssetsReportData, error) {
	if s.modelAssetsErr != nil {
		return nil, s.modelAssetsErr
	}
	if s.modelAssetsSnapshot == nil {
		return &db.ModelAssetsReportData{}, nil
	}
	return s.modelAssetsSnapshot, nil
}

func (s *stubModelsReader) KeysReportSnapshot(ctx context.Context, since time.Time) (*db.KeysReportData, error) {
	if s.keysErr != nil {
		return nil, s.keysErr
	}
	if s.keysSnapshot == nil {
		return &db.KeysReportData{}, nil
	}
	return s.keysSnapshot, nil
}

func testDependencies(reader *stubModelsReader) Dependencies {
	return Dependencies{
		Models: reader, Summary: reader, Timeseries: reader, Images: reader,
		Overview: reader, Capture: reader,
		ModelAssets: reader,
		Keys:        reader,
	}
}

type stubCostEngine struct {
	cost    float64
	known   bool
	calls   int
	model   string
	usage   pricing.TextTokenUsage
	hasText bool
}

func (s *stubCostEngine) CostText(model string, usage pricing.TextTokenUsage) (float64, bool) {
	s.calls++
	s.model = model
	s.usage = usage
	return s.cost, s.known
}

func (s *stubCostEngine) CostDimension(model, modality, channel, metric, direction, unit string, amount float64) (float64, bool) {
	return s.cost, s.known
}

func (s *stubCostEngine) CostImages(model string, inputImageCount, outputImageCount int64, size string) (float64, bool, bool) {
	return s.cost, s.known, false
}

func (s *stubCostEngine) HasMultimodal(model string) bool { return false }

func (s *stubCostEngine) HasTextPricing(model string) bool { return s.hasText }

func (s *stubCostEngine) HasPerImagePricing(model string) bool { return false }

func (s *stubCostEngine) HasImageTokenPricing(model string) bool { return false }

func TestModelsMapsAndMergesSources(t *testing.T) {
	reader := &stubModelsReader{
		snapshot: &db.ModelsReportData{Models: []db.ModelRow{{
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
			ModelReturnedSourceCounts: map[string]map[string]int64{
				"gpt-4o": {"response_body": 2},
			},
			UsageSourceCounts: map[string]map[string]int64{
				"gpt-4o": {"http_response": 1, "none": 1},
			},
			TextCostBuckets: []db.TextCostBucketRow{{
				Model: "gpt-4o", RequestInputTokens: 50, RequestCount: 2,
				InputTokens: 100, OutputTokens: 50, ReasoningTokens: 5,
				CachedTokens: 10, CacheCreationTokens: 3,
				ObservedCount: 1, MissingUsageCount: 1,
			}},
		},
	}
	cost := &stubCostEngine{cost: 1.25, known: true}
	svc := NewService(testDependencies(reader), cost)

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
	if item.CostState != CostStatePartial || len(item.PartialReasons) != 1 || item.PartialReasons[0] != PartialReasonMissingUsage {
		t.Fatalf("cost state/reasons = %s / %+v", item.CostState, item.PartialReasons)
	}
	if item.UsageConfidenceCounts.Observed != 1 || item.UsageConfidenceCounts.MissingUsage != 1 {
		t.Fatalf("confidence = %+v", item.UsageConfidenceCounts)
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
		snapshot: &db.ModelsReportData{Models: []db.ModelRow{{
			Model:        "unknown-model",
			ModelSource:  "requested",
			RequestCount: 1,
		}}},
	}
	cost := &stubCostEngine{cost: 0, known: false}
	svc := NewService(testDependencies(reader), cost)

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
	svc := NewService(testDependencies(reader), &stubCostEngine{})

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
	svc := NewService(testDependencies(reader), &stubCostEngine{})
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
		snapshot: &db.ModelsReportData{Models: []db.ModelRow{{Model: "gpt-4o"}}},
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{known: true})
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
	_ = NewService(Dependencies{}, &stubCostEngine{})
}
