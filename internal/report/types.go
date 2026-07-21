package report

type SummaryReport struct {
	TotalRequests         int64                 `json:"total_requests"`
	FailedRequests        int64                 `json:"failed_requests"`
	TotalInputTokens      int64                 `json:"total_input_tokens"`
	TotalOutputTokens     int64                 `json:"total_output_tokens"`
	TotalReasoningTokens  int64                 `json:"total_reasoning_tokens"`
	TotalCachedTokens     int64                 `json:"total_cached_tokens"`
	TotalTokens           int64                 `json:"total_tokens"`
	TotalCost             float64               `json:"total_cost"`
	CostKnown             bool                  `json:"cost_known"`
	CostState             CostState             `json:"cost_state"`
	UnpricedModels        int64                 `json:"unpriced_models"`
	PartialReasons        []PartialReason       `json:"partial_reasons"`
	UsageConfidenceCounts UsageConfidenceCounts `json:"usage_confidence_counts"`
}

type TimeseriesReport struct {
	Timestamp             string                `json:"timestamp"`
	Count                 int64                 `json:"count"`
	FailedCount           int64                 `json:"failed_count"`
	InputTokens           int64                 `json:"input_tokens"`
	OutputTokens          int64                 `json:"output_tokens"`
	ReasoningTokens       int64                 `json:"reasoning_tokens"`
	CachedTokens          int64                 `json:"cached_tokens"`
	CacheCreationTokens   int64                 `json:"cache_creation_tokens"`
	TotalTokens           int64                 `json:"total_tokens"`
	AvgLatencyMs          int64                 `json:"avg_latency_ms"`
	AvgTTFBMs             int64                 `json:"avg_ttfb_ms"`
	Cost                  float64               `json:"cost"`
	CostKnown             bool                  `json:"cost_known"`
	CostState             CostState             `json:"cost_state"`
	UnpricedModels        int64                 `json:"unpriced_models"`
	PartialReasons        []PartialReason       `json:"partial_reasons"`
	UsageConfidenceCounts UsageConfidenceCounts `json:"usage_confidence_counts"`
}

// ImageSummaryUsage is the stable nested "summary" object returned by
// /api/images/summary. It intentionally mirrors the existing JSON fields while
// remaining independent of database row types.
type ImageSummaryUsage struct {
	RequestCount      int64 `json:"request_count"`
	FailedCount       int64 `json:"failed_count"`
	ImageCount        int64 `json:"image_count"`
	PartialImageCount int64 `json:"partial_image_count"`
	InputImageCount   int64 `json:"input_image_count"`
	MissingUsageCount int64 `json:"missing_usage_count"`
	InputTextTokens   int64 `json:"input_text_tokens"`
	InputImageTokens  int64 `json:"input_image_tokens"`
	CachedTextTokens  int64 `json:"cached_text_tokens"`
	CachedImageTokens int64 `json:"cached_image_tokens"`
	CachedMixedTokens int64 `json:"cached_mixed_tokens"`
	OutputImageTokens int64 `json:"output_image_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
}

type ImageSummaryReport struct {
	Summary               ImageSummaryUsage     `json:"summary"`
	Cost                  float64               `json:"cost"`
	CostKnown             bool                  `json:"cost_known"`
	CostState             CostState             `json:"cost_state"`
	UnpricedModels        int64                 `json:"unpriced_models"`
	PartialReasons        []PartialReason       `json:"partial_reasons"`
	UsageConfidenceCounts UsageConfidenceCounts `json:"usage_confidence_counts"`
}

// ImageModelReport is the stable flat item returned by /api/images/models.
// Existing fields are preserved; cost completeness fields are additive.
type ImageModelReport struct {
	Model                 string                `json:"model"`
	Operation             string                `json:"operation"`
	RequestCount          int64                 `json:"request_count"`
	FailedCount           int64                 `json:"failed_count"`
	ImageCount            int64                 `json:"image_count"`
	PartialImageCount     int64                 `json:"partial_image_count"`
	InputImageCount       int64                 `json:"input_image_count"`
	InputTextTokens       int64                 `json:"input_text_tokens"`
	InputImageTokens      int64                 `json:"input_image_tokens"`
	CachedTextTokens      int64                 `json:"cached_text_tokens"`
	CachedImageTokens     int64                 `json:"cached_image_tokens"`
	CachedMixedTokens     int64                 `json:"cached_mixed_tokens"`
	OutputImageTokens     int64                 `json:"output_image_tokens"`
	TotalTokens           int64                 `json:"total_tokens"`
	MissingUsageCount     int64                 `json:"missing_usage_count"`
	Cost                  float64               `json:"cost"`
	CostKnown             bool                  `json:"cost_known"`
	CostState             CostState             `json:"cost_state"`
	UnpricedModels        int64                 `json:"unpriced_models"`
	PartialReasons        []PartialReason       `json:"partial_reasons"`
	UsageConfidenceCounts UsageConfidenceCounts `json:"usage_confidence_counts"`
}

// ModelReport is the stable /api/models response item.
// Field names and JSON shape match the previous event.ModelReport contract.
type ModelReport struct {
	Model                     string                `json:"model"`
	ModelSource               string                `json:"model_source"`
	RequestCount              int64                 `json:"request_count"`
	FailedCount               int64                 `json:"failed_count"`
	InputTokens               int64                 `json:"input_tokens"`
	OutputTokens              int64                 `json:"output_tokens"`
	ReasoningTokens           int64                 `json:"reasoning_tokens"`
	CachedTokens              int64                 `json:"cached_tokens"`
	CacheCreationTokens       int64                 `json:"cache_creation_tokens"`
	TotalTokens               int64                 `json:"total_tokens"`
	Cost                      float64               `json:"cost"`
	CostKnown                 bool                  `json:"cost_known"`
	CostState                 CostState             `json:"cost_state"`
	UnpricedModels            int64                 `json:"unpriced_models"`
	PartialReasons            []PartialReason       `json:"partial_reasons"`
	UsageConfidenceCounts     UsageConfidenceCounts `json:"usage_confidence_counts"`
	ModelReturnedSourceCounts map[string]int64      `json:"model_returned_source_counts,omitempty"`
	UsageSourceCounts         map[string]int64      `json:"usage_source_counts,omitempty"`
	MissingUsageCount         int64                 `json:"missing_usage_count"`
}
