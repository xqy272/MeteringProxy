package report

import (
	"context"
	"fmt"

	"ai-gateway-metering-proxy/internal/db"
)

func (s *Service) Summary(ctx context.Context, filter SummaryFilter) (SummaryReport, error) {
	if s == nil {
		return SummaryReport{}, fmt.Errorf("report service is not configured")
	}
	snapshot, err := s.summary.SummaryReportSnapshot(ctx, filter.Since)
	if err != nil {
		return SummaryReport{}, err
	}
	report := summaryReportFromRow(snapshot.Summary)
	result := completeZeroCost()
	if value, ok := evaluateCostBuckets(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, 0)[CostGroup{}]; ok {
		result = value
	}
	report.TotalCost = result.Amount
	report.CostKnown = result.CostKnown
	report.CostState = result.State
	report.UnpricedModels = result.UnpricedModels
	report.PartialReasons = nonNilPartialReasons(result.PartialReasons)
	report.UsageConfidenceCounts = result.UsageConfidenceCounts
	return report, nil
}

func (s *Service) Timeseries(ctx context.Context, filter TimeseriesFilter) ([]TimeseriesReport, error) {
	if s == nil {
		return nil, fmt.Errorf("report service is not configured")
	}
	snapshot, err := s.timeseries.TimeseriesReportSnapshot(ctx, db.ReportScope{Since: filter.Since, KeyHash: filter.KeyHash}, filter.BucketMin)
	if err != nil {
		return nil, err
	}
	costs := evaluateCostBuckets(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, costGroupByBucket)
	out := make([]TimeseriesReport, 0, len(snapshot.Rows))
	for _, row := range snapshot.Rows {
		item := timeseriesReportFromRow(row)
		result := completeZeroCost()
		if value, ok := costs[CostGroup{Bucket: row.Timestamp}]; ok {
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

func nonNilPartialReasons(reasons []PartialReason) []PartialReason {
	if reasons == nil {
		return []PartialReason{}
	}
	return reasons
}
