package report

import "ai-gateway-metering-proxy/internal/db"

func modelReportFromRow(row db.ModelRow) ModelReport {
	return ModelReport{
		Model:               row.Model,
		ModelSource:         row.ModelSource,
		RequestCount:        row.RequestCount,
		FailedCount:         row.FailedCount,
		InputTokens:         row.InputTokens,
		OutputTokens:        row.OutputTokens,
		ReasoningTokens:     row.ReasoningTokens,
		CachedTokens:        row.CachedTokens,
		CacheCreationTokens: row.CacheCreationTokens,
		TotalTokens:         row.TotalTokens,
		MissingUsageCount:   row.MissingUsageCount,
	}
}

func mergeModelSourceCounts(
	report *ModelReport,
	returnedByModel map[string]map[string]int64,
	usageByModel map[string]map[string]int64,
) {
	if counts := returnedByModel[report.Model]; len(counts) > 0 {
		report.ModelReturnedSourceCounts = counts
	}
	if counts := usageByModel[report.Model]; len(counts) > 0 {
		report.UsageSourceCounts = counts
	}
}
