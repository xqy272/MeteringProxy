package report

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func (s *Service) ModelAssets(ctx context.Context, filter ModelAssetsFilter) (ModelAssetsReport, error) {
	if s == nil {
		return ModelAssetsReport{}, fmt.Errorf("report service is not configured")
	}
	snapshot, err := s.modelAssets.ModelAssetsReportSnapshot(ctx, filter.Since)
	if err != nil {
		return ModelAssetsReport{}, err
	}
	costs := evaluateCostBuckets(s.cost, snapshot.TextCostBuckets, snapshot.ImageCostBuckets, costGroupByModel)
	out := ModelAssetsReport{Range: filter.Range, Items: make([]ModelAssetItem, 0, len(snapshot.Rows))}
	for _, row := range snapshot.Rows {
		result := completeZeroCost()
		if value, ok := costs[CostGroup{Model: row.Model}]; ok {
			result = value
		}
		configured := s.cost.HasTextPricing(row.Model) || s.cost.HasMultimodal(row.Model)
		pricingSource := "unpriced"
		if configured {
			pricingSource = "exact"
		}
		sources := splitSortedCSV(row.ModelSources)
		if len(sources) == 0 {
			sources = []string{"requested"}
		}
		if configured {
			sources = append(sources, "pricing")
		}
		item := ModelAssetItem{
			Model: row.Model, Sources: sources,
			EndpointProfiles: splitSortedCSV(row.EndpointProfiles),
			CaptureMode:      modelAssetCaptureMode(result.UsageConfidenceCounts),
			Confidence:       modelAssetConfidence(result.UsageConfidenceCounts),
			RequestCount:     row.RequestCount, FailedCount: row.FailedCount,
			InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
			ReasoningTokens: row.ReasoningTokens, CachedTokens: row.CachedTokens,
			CacheCreationTokens: row.CacheCreationTokens, TotalTokens: row.TotalTokens,
			EstimatedCost: result.Amount, CostKnown: result.CostKnown, CostState: result.State,
			UnpricedModels: result.UnpricedModels, PartialReasons: nonNilPartialReasons(result.PartialReasons),
			UsageConfidenceCounts: result.UsageConfidenceCounts,
			PricingSource:         pricingSource, LatestSeenAt: row.LatestSeenAt,
		}
		out.Items = append(out.Items, item)
		out.Summary.ModelsTotal++
		out.Summary.UsedModels++
		if !configured {
			out.Summary.UnpricedUsedModels++
		}
		if result.UsageConfidenceCounts.RequestOnly > 0 {
			out.Summary.RequestOnlyModels++
		}
		if result.State != CostStateComplete || !configured {
			out.Summary.CostPartial = true
		}
	}
	return out, nil
}

func splitSortedCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	seen := make(map[string]struct{})
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			seen[part] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func modelAssetCaptureMode(counts UsageConfidenceCounts) string {
	modes := 0
	value := "unknown"
	if counts.Observed+counts.SideChannel+counts.MissingUsage+counts.Conflict > 0 {
		modes++
		value = "usage_metered"
	}
	if counts.RequestOnly > 0 {
		modes++
		value = "request_only"
	}
	if counts.Unsupported > 0 {
		modes++
		value = "passthrough"
	}
	if modes > 1 {
		return "mixed"
	}
	return value
}

func modelAssetConfidence(counts UsageConfidenceCounts) string {
	switch {
	case counts.Conflict > 0:
		return "conflict"
	case counts.Unsupported > 0:
		return "unsupported"
	case counts.RequestOnly > 0:
		return "request_only"
	case counts.MissingUsage > 0:
		return "missing_usage"
	case counts.SideChannel > 0 && counts.Observed == 0:
		return "side_channel"
	case counts.Observed > 0 || counts.SideChannel > 0:
		return "observed"
	default:
		return "missing_usage"
	}
}
