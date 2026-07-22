package report

import (
	"ai-gateway-metering-proxy/internal/metrics"
	"context"
	"fmt"
)

func (s *Service) Keys(ctx context.Context, filter KeysFilter) ([]KeyReport, error) {
	return observeReport(metrics.ReportKeys, func() ([]KeyReport, error) {
		if s == nil {
			return nil, fmt.Errorf("report service is not configured")
		}
		snapshot, err := s.keys.KeysReportSnapshot(ctx, filter.Since)
		if err != nil {
			return nil, err
		}
		costs := evaluateCostBuckets(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, costGroupByKey)
		out := make([]KeyReport, 0, len(snapshot.Rows))
		for _, row := range snapshot.Rows {
			result := completeZeroCost()
			if value, ok := costs[CostGroup{KeyHash: row.KeyHash}]; ok {
				result = value
			}
			out = append(out, KeyReport{
				KeyHash: row.KeyHash, Label: s.keyLabels[row.KeyHash],
				RequestCount: row.RequestCount, FailedCount: row.FailedCount,
				FailureRate: failureRate(row.FailedCount, row.RequestCount), ModelCount: row.ModelCount,
				InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
				ReasoningTokens: row.ReasoningTokens, CachedTokens: row.CachedTokens,
				CacheCreationTokens: row.CacheCreationTokens, TotalTokens: row.TotalTokens,
				AvgLatencyMs: row.AvgLatencyMs, AvgTTFBMs: row.AvgTTFBMs,
				EstimatedCost: result.Amount, CostKnown: result.CostKnown, CostState: result.State,
				UnpricedModels: result.UnpricedModels, MissingUsageCount: result.MissingUsageCount,
				PartialReasons:        nonNilPartialReasons(result.PartialReasons),
				UsageConfidenceCounts: result.UsageConfidenceCounts,
				LatestSeenAt:          row.LatestSeenAt,
			})
		}
		return out, nil
	})
}
