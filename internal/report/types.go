package report

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
