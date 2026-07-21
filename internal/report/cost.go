package report

import "ai-gateway-metering-proxy/internal/db"

func hasBillableTextUsage(row db.ModelRow) bool {
	return row.InputTokens > 0 ||
		row.OutputTokens > 0 ||
		row.ReasoningTokens > 0 ||
		row.CachedTokens > 0 ||
		row.CacheCreationTokens > 0 ||
		row.TotalTokens > 0
}

func applyModelCost(engine CostEngine, report *ModelReport, row db.ModelRow) {
	cost, known := engine.CostWithCacheCreation(
		row.Model,
		row.InputTokens,
		row.OutputTokens,
		row.ReasoningTokens,
		row.CachedTokens,
		row.CacheCreationTokens,
	)
	report.Cost = cost
	report.CostKnown = known || !hasBillableTextUsage(row)
}
