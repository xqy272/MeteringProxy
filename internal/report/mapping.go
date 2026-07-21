package report

import "ai-gateway-metering-proxy/internal/db"

func summaryReportFromRow(row db.SummaryRow) SummaryReport {
	return SummaryReport{
		TotalRequests:        row.TotalRequests,
		FailedRequests:       row.FailedRequests,
		TotalInputTokens:     row.TotalInputTokens,
		TotalOutputTokens:    row.TotalOutputTokens,
		TotalReasoningTokens: row.TotalReasoningTokens,
		TotalCachedTokens:    row.TotalCachedTokens,
		TotalTokens:          row.TotalTokens,
	}
}

func timeseriesReportFromRow(row db.TimeseriesRow) TimeseriesReport {
	return TimeseriesReport{
		Timestamp:           row.Timestamp,
		Count:               row.Count,
		FailedCount:         row.FailedCount,
		InputTokens:         row.InputTokens,
		OutputTokens:        row.OutputTokens,
		ReasoningTokens:     row.ReasoningTokens,
		CachedTokens:        row.CachedTokens,
		CacheCreationTokens: row.CacheCreationTokens,
		TotalTokens:         row.TotalTokens,
		AvgLatencyMs:        row.AvgLatencyMs,
		AvgTTFBMs:           row.AvgTTFBMs,
	}
}

func imageSummaryUsageFromRow(row db.ImageSummaryRow) ImageSummaryUsage {
	return ImageSummaryUsage{
		RequestCount:      row.RequestCount,
		FailedCount:       row.FailedCount,
		ImageCount:        row.ImageCount,
		PartialImageCount: row.PartialImageCount,
		InputImageCount:   row.InputImageCount,
		MissingUsageCount: row.MissingUsageCount,
		InputTextTokens:   row.InputTextTokens,
		InputImageTokens:  row.InputImageTokens,
		CachedTextTokens:  row.CachedTextTokens,
		CachedImageTokens: row.CachedImageTokens,
		CachedMixedTokens: row.CachedMixedTokens,
		OutputImageTokens: row.OutputImageTokens,
		TotalTokens:       row.TotalTokens,
	}
}

func imageModelReportFromRow(row db.ImageModelRow) ImageModelReport {
	return ImageModelReport{
		Model:             row.Model,
		Operation:         row.Operation,
		RequestCount:      row.RequestCount,
		FailedCount:       row.FailedCount,
		ImageCount:        row.ImageCount,
		PartialImageCount: row.PartialImageCount,
		InputImageCount:   row.InputImageCount,
		InputTextTokens:   row.InputTextTokens,
		InputImageTokens:  row.InputImageTokens,
		CachedTextTokens:  row.CachedTextTokens,
		CachedImageTokens: row.CachedImageTokens,
		CachedMixedTokens: row.CachedMixedTokens,
		OutputImageTokens: row.OutputImageTokens,
		TotalTokens:       row.TotalTokens,
		MissingUsageCount: row.MissingUsageCount,
	}
}

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
