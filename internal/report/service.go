package report

import (
	"context"
	"fmt"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

// Service orchestrates read-side report assembly for WebUI handlers.
// It implements the core usage/cost reporter interfaces.
type Service struct {
	models      ModelsReader
	summary     SummaryReader
	timeseries  TimeseriesReader
	images      ImagesReader
	overview    OverviewReader
	capture     CaptureRuntimeReader
	modelAssets ModelAssetsReader
	keys        KeysReader
	activity    ActivityReader
	requests    RequestsReader
	keyLabels   map[string]string
	cost        CostEngine
	now         func() time.Time
}

// NewService constructs a report service from narrow reader/pricing interfaces.
func NewService(deps Dependencies, cost CostEngine) *Service {
	if deps.Models == nil {
		panic("report: ModelsReader is required")
	}
	if deps.Summary == nil {
		panic("report: SummaryReader is required")
	}
	if deps.Timeseries == nil {
		panic("report: TimeseriesReader is required")
	}
	if deps.Images == nil {
		panic("report: ImagesReader is required")
	}
	if deps.Overview == nil {
		panic("report: OverviewReader is required")
	}
	if deps.Capture == nil {
		panic("report: CaptureRuntimeReader is required")
	}
	if deps.ModelAssets == nil {
		panic("report: ModelAssetsReader is required")
	}
	if deps.Keys == nil {
		panic("report: KeysReader is required")
	}
	if deps.Activity == nil {
		panic("report: ActivityReader is required")
	}
	if deps.Requests == nil {
		panic("report: RequestsReader is required")
	}
	if cost == nil {
		panic("report: CostEngine is required")
	}
	labels := make(map[string]string, len(deps.KeyLabels))
	for hash, label := range deps.KeyLabels {
		labels[hash] = label
	}
	return &Service{
		models: deps.Models, summary: deps.Summary, timeseries: deps.Timeseries,
		images: deps.Images, overview: deps.Overview, capture: deps.Capture,
		modelAssets: deps.ModelAssets,
		keys:        deps.Keys, activity: deps.Activity, requests: deps.Requests, keyLabels: labels,
		cost: cost, now: time.Now,
	}
}

// Models builds the /api/models report from one consistent DB snapshot, then
// applies pricing. Any snapshot read failure fails the whole report (atomic).
// Source breakdown is not exposed as optional/partial in this slice because
// /api/models returns a bare JSON array with no additive partial envelope.
func (s *Service) Models(ctx context.Context, filter ModelsFilter) ([]ModelReport, error) {
	if s == nil {
		return nil, fmt.Errorf("report service is not configured")
	}

	snapshot, err := s.models.ModelsReportSnapshot(ctx, db.ReportScope{Since: filter.Since, KeyHash: filter.KeyHash})
	if err != nil {
		return nil, err
	}
	costs := evaluateCostBuckets(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, costGroupByModel)

	out := make([]ModelReport, 0, len(snapshot.Models))
	for _, row := range snapshot.Models {
		item := modelReportFromRow(row)
		mergeModelSourceCounts(&item, snapshot.ModelReturnedSourceCounts, snapshot.UsageSourceCounts)
		costResult := completeZeroCost()
		if result, ok := costs[CostGroup{Model: row.Model}]; ok {
			costResult = result
		}
		applyModelCostResult(&item, costResult)
		out = append(out, item)
	}
	return out, nil
}
