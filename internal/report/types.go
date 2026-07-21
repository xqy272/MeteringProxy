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

type SectionStatus string

const (
	SectionStatusComplete    SectionStatus = "complete"
	SectionStatusPartial     SectionStatus = "partial"
	SectionStatusUnavailable SectionStatus = "unavailable"
)

type OverviewSelectedData struct {
	TotalRequests        int64   `json:"total_requests"`
	FailedRequests       int64   `json:"failed_requests"`
	FailureRate          float64 `json:"failure_rate"`
	TotalInputTokens     int64   `json:"total_input_tokens"`
	TotalOutputTokens    int64   `json:"total_output_tokens"`
	TotalReasoningTokens int64   `json:"total_reasoning_tokens"`
	TotalCachedTokens    int64   `json:"total_cached_tokens"`
	TotalTokens          int64   `json:"total_tokens"`
	TotalCost            float64 `json:"total_cost"`
	P95LatencyMs         int64   `json:"p95_latency_ms"`
	P95TTFBMs            int64   `json:"p95_ttfb_ms"`
}

type OverviewLatestError struct {
	LatestAt    string `json:"latest_at"`
	Status      int    `json:"status"`
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	ModelSource string `json:"model_source"`
	Class       string `json:"class"`
	Message     string `json:"message"`
	RequestID   string `json:"request_id"`
}

type OverviewRecentData struct {
	TotalRequests  int64                `json:"total_requests"`
	FailedRequests int64                `json:"failed_requests"`
	FailureRate    float64              `json:"failure_rate"`
	P95LatencyMs   int64                `json:"p95_latency_ms"`
	LatestError    *OverviewLatestError `json:"latest_error"`
}

type OverviewCaptureData struct {
	Status         string `json:"status"`
	QueueDepth     int64  `json:"queue_depth"`
	DroppedEvents  int64  `json:"dropped_events"`
	ParseErrors    int64  `json:"parse_errors"`
	DBWriteErrors  int64  `json:"db_write_errors"`
	CaptureFailed  int64  `json:"capture_failed"`
	CaptureSkipped int64  `json:"capture_skipped"`
}

type OverviewCostData struct {
	KnownCost             float64               `json:"known_cost"`
	UnpricedModels        int64                 `json:"unpriced_models"`
	Partial               bool                  `json:"partial"`
	CostKnown             bool                  `json:"cost_known"`
	CostState             CostState             `json:"cost_state"`
	PartialReasons        []PartialReason       `json:"partial_reasons"`
	UsageConfidenceCounts UsageConfidenceCounts `json:"usage_confidence_counts"`
}

type OverviewSelectedSection struct {
	Data      OverviewSelectedData `json:"data"`
	Error     string               `json:"error"`
	Status    SectionStatus        `json:"status"`
	ErrorCode string               `json:"error_code"`
}

type OverviewRecentSection struct {
	Data      OverviewRecentData `json:"data"`
	Error     string             `json:"error"`
	Status    SectionStatus      `json:"status"`
	ErrorCode string             `json:"error_code"`
}

type OverviewCaptureSection struct {
	Data      OverviewCaptureData `json:"data"`
	Error     string              `json:"error"`
	Status    SectionStatus       `json:"status"`
	ErrorCode string              `json:"error_code"`
}

type OverviewCostSection struct {
	Data      OverviewCostData `json:"data"`
	Error     string           `json:"error"`
	Status    SectionStatus    `json:"status"`
	ErrorCode string           `json:"error_code"`
}

type OverviewReport struct {
	Range    string                  `json:"range"`
	Selected OverviewSelectedSection `json:"selected"`
	Recent1h OverviewRecentSection   `json:"recent_1h"`
	Capture  OverviewCaptureSection  `json:"capture"`
	Cost     OverviewCostSection     `json:"cost"`
}

type ModelAssetsReport struct {
	Range   string            `json:"range"`
	Items   []ModelAssetItem  `json:"items"`
	Summary ModelAssetSummary `json:"summary"`
}

type ModelAssetItem struct {
	Model                 string                `json:"model"`
	Sources               []string              `json:"sources"`
	EndpointProfiles      []string              `json:"endpoint_profiles"`
	CaptureMode           string                `json:"capture_mode"`
	Confidence            string                `json:"confidence"`
	RequestCount          int64                 `json:"request_count"`
	FailedCount           int64                 `json:"failed_count"`
	InputTokens           int64                 `json:"input_tokens"`
	OutputTokens          int64                 `json:"output_tokens"`
	ReasoningTokens       int64                 `json:"reasoning_tokens"`
	CachedTokens          int64                 `json:"cached_tokens"`
	CacheCreationTokens   int64                 `json:"cache_creation_tokens"`
	TotalTokens           int64                 `json:"total_tokens"`
	EstimatedCost         float64               `json:"estimated_cost"`
	CostKnown             bool                  `json:"cost_known"`
	CostState             CostState             `json:"cost_state"`
	UnpricedModels        int64                 `json:"unpriced_models"`
	PartialReasons        []PartialReason       `json:"partial_reasons"`
	UsageConfidenceCounts UsageConfidenceCounts `json:"usage_confidence_counts"`
	PricingSource         string                `json:"pricing_source"`
	LatestSeenAt          string                `json:"latest_seen_at"`
}

type ModelAssetSummary struct {
	ModelsTotal        int  `json:"models_total"`
	UsedModels         int  `json:"used_models"`
	UnpricedUsedModels int  `json:"unpriced_used_models"`
	RequestOnlyModels  int  `json:"request_only_models"`
	CostPartial        bool `json:"cost_partial"`
}

type KeyReport struct {
	KeyHash               string                `json:"key_hash"`
	Label                 string                `json:"label"`
	RequestCount          int64                 `json:"request_count"`
	FailedCount           int64                 `json:"failed_count"`
	FailureRate           float64               `json:"failure_rate"`
	ModelCount            int64                 `json:"model_count"`
	InputTokens           int64                 `json:"input_tokens"`
	OutputTokens          int64                 `json:"output_tokens"`
	ReasoningTokens       int64                 `json:"reasoning_tokens"`
	CachedTokens          int64                 `json:"cached_tokens"`
	CacheCreationTokens   int64                 `json:"cache_creation_tokens"`
	TotalTokens           int64                 `json:"total_tokens"`
	AvgLatencyMs          int64                 `json:"avg_latency_ms"`
	AvgTTFBMs             int64                 `json:"avg_ttfb_ms"`
	EstimatedCost         float64               `json:"estimated_cost"`
	CostKnown             bool                  `json:"cost_known"`
	CostState             CostState             `json:"cost_state"`
	UnpricedModels        int64                 `json:"unpriced_models"`
	MissingUsageCount     int64                 `json:"missing_usage_count"`
	PartialReasons        []PartialReason       `json:"partial_reasons"`
	UsageConfidenceCounts UsageConfidenceCounts `json:"usage_confidence_counts"`
	LatestSeenAt          string                `json:"latest_seen_at"`
}

type ActivityReport struct {
	SampleSize          int64   `json:"sample_size"`
	SuccessCount        int64   `json:"success_count"`
	FailedCount         int64   `json:"failed_count"`
	FailureRate         float64 `json:"failure_rate"`
	AvgLatencyMs        int64   `json:"avg_latency_ms"`
	P95LatencyMs        int64   `json:"p95_latency_ms"`
	AvgTTFBMs           int64   `json:"avg_ttfb_ms"`
	P95TTFBMs           int64   `json:"p95_ttfb_ms"`
	CaptureCaptured     int64   `json:"capture_captured"`
	CaptureFailed       int64   `json:"capture_failed"`
	CaptureSkipped      int64   `json:"capture_skipped"`
	LatestErrorStatus   int     `json:"latest_error_status"`
	LatestErrorAt       string  `json:"latest_error_at"`
	LatestError         string  `json:"latest_error"`
	LatestErrorClass    string  `json:"latest_error_class"`
	LatestErrorCode     string  `json:"latest_error_code"`
	LatestErrorEndpoint string  `json:"latest_error_endpoint"`
	LatestErrorModel    string  `json:"latest_error_model"`
}

type RequestReport struct {
	ID                    int64  `json:"id"`
	CreatedAt             string `json:"created_at"`
	RequestID             string `json:"request_id"`
	Endpoint              string `json:"endpoint"`
	EndpointProfile       string `json:"endpoint_profile"`
	CaptureMode           string `json:"capture_mode"`
	MeteringKind          string `json:"metering_kind"`
	Method                string `json:"method"`
	Status                int    `json:"status"`
	LatencyMs             int64  `json:"latency_ms"`
	TTFBMs                int64  `json:"ttfb_ms"`
	Stream                bool   `json:"stream"`
	ClientIPHash          string `json:"client_ip_hash"`
	APIKeyHash            string `json:"api_key_hash"`
	ModelRequested        string `json:"model_requested"`
	ModelReturned         string `json:"model_returned"`
	InputTokens           int64  `json:"input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	ReasoningTokens       int64  `json:"reasoning_tokens"`
	CachedTokens          int64  `json:"cached_tokens"`
	CacheCreationTokens   int64  `json:"cache_creation_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	RequestBytes          int64  `json:"request_bytes"`
	ResponseBytes         int64  `json:"response_bytes"`
	CaptureOutcome        string `json:"capture_outcome"`
	CaptureReason         string `json:"capture_reason"`
	Error                 string `json:"error"`
	ErrorClass            string `json:"error_class"`
	ErrorType             string `json:"error_type"`
	ErrorCode             string `json:"error_code"`
	ErrorParam            string `json:"error_param"`
	ErrorMessage          string `json:"error_message"`
	ErrorMessageTruncated bool   `json:"error_message_truncated"`
	ModelReturnedSource   string `json:"model_returned_source"`
	UsageSource           string `json:"usage_source"`
	TerminalEvent         string `json:"terminal_event"`
	TerminalReason        string `json:"terminal_reason"`
	SideUsageEventID      int64  `json:"side_usage_event_id"`
	SideUsageMatchStatus  string `json:"side_usage_match_status"`
	UsageConfidence       string `json:"usage_confidence"`
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

// Issue source status values for multi-source issues reports.
const (
	IssueSourceComplete      = "complete"
	IssueSourceUnavailable   = "unavailable"
	IssueSourceNotApplicable = "not_applicable"
)

type IssueReport struct {
	Class       string `json:"class"`
	Label       string `json:"label"`
	Count       int64  `json:"count"`
	Severity    string `json:"severity"`
	SourceGroup string `json:"source_group"`
	LatestAt    string `json:"latest_at"`
	Status      int    `json:"status"`
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	ModelSource string `json:"model_source"`
	APIKeyHash  string `json:"api_key_hash"`
	ErrorType   string `json:"error_type"`
	ErrorCode   string `json:"error_code"`
	Message     string `json:"message"`
	RequestID   string `json:"request_id"`
}

type IssuesSystem struct {
	ParseErrors   int64             `json:"parse_errors"`
	DBErrors      int64             `json:"db_errors"`
	DroppedEvents int64             `json:"dropped_events"`
	Items         []IssueSystemItem `json:"items"`
}

type IssueSystemItem struct {
	Class    string `json:"class"`
	Label    string `json:"label"`
	Count    int64  `json:"count"`
	Scope    string `json:"scope"`
	Severity string `json:"severity"`
}

// IssuesSourceStatuses is the additive multi-source status envelope for /api/issues.
type IssuesSourceStatuses struct {
	RequestUsage     string `json:"request_usage"`
	SideChannel      string `json:"side_channel"`
	CredentialHealth string `json:"credential_health"`
	Quota            string `json:"quota"`
	System           string `json:"system"`
}

// IssuesReport is the stable /api/issues response. Old fields are preserved;
// Partial and Sources are additive.
type IssuesReport struct {
	Range   string               `json:"range"`
	Total   int                  `json:"total"`
	Items   []IssueReport        `json:"items"`
	System  IssuesSystem         `json:"system"`
	Partial bool                 `json:"partial"`
	Sources IssuesSourceStatuses `json:"sources"`
}

// MultimodalSummaryReport is the stable /api/multimodal/summary item.
type MultimodalSummaryReport struct {
	Modality      string  `json:"modality"`
	Channel       string  `json:"channel"`
	Metric        string  `json:"metric"`
	Direction     string  `json:"direction"`
	Unit          string  `json:"unit"`
	Amount        float64 `json:"amount"`
	RequestCount  int64   `json:"request_count"`
	UnpricedCount int64   `json:"unpriced_count"`
}

// ErrorTimelinePoint is one combined errors timeline bucket.
type ErrorTimelinePoint struct {
	Timestamp       string `json:"timestamp"`
	Count           int64  `json:"count"`
	ParseErrors     int64  `json:"parse_errors"`
	DBErrors        int64  `json:"db_errors"`
	DroppedEvents   int64  `json:"dropped_events"`
	BaselineMissing bool   `json:"baseline_missing,omitempty"`
}

// Source status values for multi-source dashboard reports.
const (
	SourceComplete      = "complete"
	SourceUnavailable   = "unavailable"
	SourceEmpty         = "empty"
	SourceNotApplicable = "not_applicable"
)

// ErrorsSourceStatuses is the additive multi-source status envelope for /api/errors.
type ErrorsSourceStatuses struct {
	HealthMetrics string `json:"health_metrics"`
	RequestUsage  string `json:"request_usage"`
	LatestHealth  string `json:"latest_health"`
}

// ErrorsReport is the stable /api/errors response. Old fields are preserved;
// Partial and SourceStatuses are additive.
type ErrorsReport struct {
	Timeline           []ErrorTimelinePoint `json:"timeline"`
	Source             string               `json:"source"`
	BucketCount        int                  `json:"bucket_count"`
	NonzeroBucketCount int                  `json:"nonzero_bucket_count"`
	QueueDepth         int64                `json:"queue_depth"`
	ParseErrors        int64                `json:"parse_errors"`
	DBErrors           int64                `json:"db_errors"`
	DroppedEvents      int64                `json:"dropped_events"`
	Partial            bool                 `json:"partial"`
	SourceStatuses     ErrorsSourceStatuses `json:"source_statuses"`
}

// HealthSnapshot is the nested latest_health object in /api/health.
type HealthSnapshot struct {
	Timestamp     string `json:"timestamp"`
	QueueDepth    int64  `json:"queue_depth"`
	DroppedEvents int64  `json:"dropped_events"`
	ParseErrors   int64  `json:"parse_errors"`
	DBErrors      int64  `json:"db_errors"`
	SSELineSkips  int64  `json:"sse_line_skips"`
}

// HealthSourceStatuses is the additive multi-source status for /api/health.
type HealthSourceStatuses struct {
	Runtime      string `json:"runtime"`
	LatestHealth string `json:"latest_health"`
}

// HealthDashboardReport is the stable /api/health response.
type HealthDashboardReport struct {
	QueueDepth      int64                `json:"queue_depth"`
	DroppedEvents   int64                `json:"dropped_events"`
	ParseErrors     int64                `json:"parse_errors"`
	DBWriteErrors   int64                `json:"db_write_errors"`
	LatestHealth    HealthSnapshot       `json:"latest_health"`
	MeteringEnabled bool                 `json:"metering_enabled"`
	CaptureDisabled bool                 `json:"capture_disabled"`
	Partial         bool                 `json:"partial"`
	SourceStatuses  HealthSourceStatuses `json:"source_statuses"`
}

// RangeMeta is a fixed time-range option for metadata.
type RangeMeta struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Bucket string `json:"bucket"`
}

// BucketMeta is a fixed bucket option for metadata.
type BucketMeta struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// EndpointMeta is the metadata API endpoint description.
// Defined here for report JSON stability; profile.EndpointMeta is the source shape.
type EndpointMeta struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	FilterValue  string `json:"filter_value"`
	Method       string `json:"method"`
	DisplayName  string `json:"display_name"`
	MeteringKind string `json:"metering_kind"`
	CaptureMode  string `json:"capture_mode"`
}

// MetadataReport is the stable /api/metadata response.
type MetadataReport struct {
	Endpoints     []EndpointMeta `json:"endpoints"`
	Ranges        []RangeMeta    `json:"ranges"`
	Buckets       []BucketMeta   `json:"buckets"`
	MeteringKinds []string       `json:"metering_kinds"`
	CaptureModes  []string       `json:"capture_modes"`
}

// GatewayCapabilitySummary is the summary block of /api/gateway/capabilities.
type GatewayCapabilitySummary struct {
	TotalRequests    int64 `json:"total_requests"`
	UsageMeteredReqs int64 `json:"usage_metered_requests"`
	RequestOnlyReqs  int64 `json:"request_only_requests"`
	PassthroughReqs  int64 `json:"passthrough_requests"`
	StreamRequests   int64 `json:"stream_requests"`
	MissingUsageReqs int64 `json:"missing_usage_requests"`
}

// GatewayCapabilityProfile is one profile row in /api/gateway/capabilities.
type GatewayCapabilityProfile struct {
	Name              string   `json:"name"`
	DisplayName       string   `json:"display_name"`
	CaptureMode       string   `json:"capture_mode"`
	MeteringKind      string   `json:"metering_kind"`
	RequestCount      int64    `json:"request_count"`
	MissingUsageCount int64    `json:"missing_usage_count"`
	StreamCount       int64    `json:"stream_count"`
	KnownLimitations  []string `json:"known_limitations,omitempty"`
}

// GatewayCapabilitiesReport is the stable /api/gateway/capabilities response.
type GatewayCapabilitiesReport struct {
	Range    string                     `json:"range"`
	Summary  GatewayCapabilitySummary   `json:"summary"`
	Profiles []GatewayCapabilityProfile `json:"profiles"`
}
