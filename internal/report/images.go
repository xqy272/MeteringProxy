package report

import (
	"context"
	"fmt"
)

func (s *Service) ImageSummary(ctx context.Context, filter ImagesFilter) (ImageSummaryReport, error) {
	if s == nil {
		return ImageSummaryReport{}, fmt.Errorf("report service is not configured")
	}
	snapshot, err := s.images.ImageReportSnapshot(ctx, filter.Since)
	if err != nil {
		return ImageSummaryReport{}, err
	}

	result := completeZeroCost()
	if value, ok := evaluateCostBuckets(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, 0)[CostGroup{}]; ok {
		result = value
	}
	usage := imageSummaryUsageFromRow(snapshot.Summary)
	usage.MissingUsageCount = result.MissingUsageCount
	return ImageSummaryReport{
		Summary:               usage,
		Cost:                  result.Amount,
		CostKnown:             result.CostKnown,
		CostState:             result.State,
		UnpricedModels:        result.UnpricedModels,
		PartialReasons:        nonNilPartialReasons(result.PartialReasons),
		UsageConfidenceCounts: result.UsageConfidenceCounts,
	}, nil
}

func (s *Service) ImageModels(ctx context.Context, filter ImagesFilter) ([]ImageModelReport, error) {
	if s == nil {
		return nil, fmt.Errorf("report service is not configured")
	}
	snapshot, err := s.images.ImageReportSnapshot(ctx, filter.Since)
	if err != nil {
		return nil, err
	}

	grouping := costGroupByModel | costGroupByOperation
	costs := evaluateCostBuckets(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, grouping)
	out := make([]ImageModelReport, 0, len(snapshot.Models))
	for _, row := range snapshot.Models {
		group := CostGroup{Model: row.Model, Operation: row.Operation}
		item := imageModelReportFromRow(row)
		result := completeZeroCost()
		if value, ok := costs[group]; ok {
			result = value
		}
		item.MissingUsageCount = result.MissingUsageCount
		item.Cost = result.Amount
		item.CostKnown = result.CostKnown
		item.CostState = result.State
		item.UnpricedModels = result.UnpricedModels
		item.PartialReasons = nonNilPartialReasons(result.PartialReasons)
		item.UsageConfidenceCounts = result.UsageConfidenceCounts
		out = append(out, item)
	}
	return out, nil
}
