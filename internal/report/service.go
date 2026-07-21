package report

import (
	"context"
	"fmt"
)

// Service orchestrates read-side report assembly for WebUI handlers.
// It implements ModelsReporter.
type Service struct {
	models ModelsReader
	cost   CostEngine
}

// NewService constructs a report service from narrow reader/pricing interfaces.
func NewService(models ModelsReader, cost CostEngine) *Service {
	if models == nil {
		panic("report: ModelsReader is required")
	}
	if cost == nil {
		panic("report: CostEngine is required")
	}
	return &Service{models: models, cost: cost}
}

// Models builds the /api/models report from one consistent DB snapshot, then
// applies pricing. Any snapshot read failure fails the whole report (atomic).
// Source breakdown is not exposed as optional/partial in this slice because
// /api/models returns a bare JSON array with no additive partial envelope.
func (s *Service) Models(ctx context.Context, filter ModelsFilter) ([]ModelReport, error) {
	if s == nil {
		return nil, fmt.Errorf("report service is not configured")
	}

	snapshot, err := s.models.ModelsReportSnapshot(ctx, filter.Since)
	if err != nil {
		return nil, err
	}
	costs := evaluateCostBuckets(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets)

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
