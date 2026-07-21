package report

import (
	"context"
	"fmt"

	"ai-gateway-metering-proxy/internal/db"
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
	missing := imageMissingUsageCounts(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, 0)[CostGroup{}]
	usage := imageSummaryUsageFromRow(snapshot.Summary)
	usage.MissingUsageCount = missing
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
	missing := imageMissingUsageCounts(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, grouping)
	out := make([]ImageModelReport, 0, len(snapshot.Models))
	for _, row := range snapshot.Models {
		group := CostGroup{Model: row.Model, Operation: row.Operation}
		item := imageModelReportFromRow(row)
		item.MissingUsageCount = missing[group]
		result := completeZeroCost()
		if value, ok := costs[group]; ok {
			result = value
		}
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

// imageMissingUsageCounts preserves the legacy image report field while
// deriving it from the configured billing channel. Per-image-only models do
// not require token dimensions; token-priced image models do.
func imageMissingUsageCounts(engine CostEngine, textRows []db.TextCostBucketRow, imageRows []db.ImageCostBucketRow, grouping costGrouping) map[CostGroup]int64 {
	result := make(map[CostGroup]int64)
	for _, row := range imageRows {
		group := makeCostGroup(grouping, row.Bucket, row.KeyHash, row.Model, row.Operation)
		result[group] += row.MissingUsageCount
		if engine.HasMultimodal(row.Model) && engine.HasImageTokenPricing(row.Model) {
			expected := row.ObservedCount + row.SideChannelCount
			if row.TokenUsageCount < expected {
				result[group] += expected - row.TokenUsageCount
			}
		}
	}
	for _, row := range textRows {
		if !row.ImageRequest || engine.HasMultimodal(row.Model) {
			continue
		}
		group := makeCostGroup(grouping, row.Bucket, row.KeyHash, row.Model, row.Operation)
		expected := row.ObservedCount + row.SideChannelCount
		if row.BillableUsageCount < expected {
			result[group] += expected - row.BillableUsageCount
		}
	}
	return result
}
