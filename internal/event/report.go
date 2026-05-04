package event

// Report types are used by the query/reporting layer. They are independent of
// the database schema and provide stable types for WebUI API responses.

type SummaryReport struct {
	TotalRequests        int64   `json:"total_requests"`
	FailedRequests       int64   `json:"failed_requests"`
	TotalInputTokens     int64   `json:"total_input_tokens"`
	TotalOutputTokens    int64   `json:"total_output_tokens"`
	TotalReasoningTokens int64   `json:"total_reasoning_tokens"`
	TotalCachedTokens    int64   `json:"total_cached_tokens"`
	TotalTokens          int64   `json:"total_tokens"`
	TotalCost            float64 `json:"total_cost"`
}

type ModelReport struct {
	Model               string  `json:"model"`
	ModelSource         string  `json:"model_source"`
	RequestCount        int64   `json:"request_count"`
	FailedCount         int64   `json:"failed_count"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	ReasoningTokens     int64   `json:"reasoning_tokens"`
	CachedTokens        int64   `json:"cached_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	Cost                float64 `json:"cost"`
	CostKnown           bool    `json:"cost_known"`
}

type KeyReport struct {
	KeyHash      string `json:"key_hash"`
	RequestCount int64  `json:"request_count"`
	FailedCount  int64  `json:"failed_count"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

type TimeseriesReport struct {
	Timestamp           string  `json:"timestamp"`
	Count               int64   `json:"count"`
	FailedCount         int64   `json:"failed_count"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	ReasoningTokens     int64   `json:"reasoning_tokens"`
	CachedTokens        int64   `json:"cached_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	AvgLatencyMs        int64   `json:"avg_latency_ms"`
	AvgTTFBMs           int64   `json:"avg_ttfb_ms"`
	Cost                float64 `json:"cost"`
	CostKnown           bool    `json:"cost_known"`
	UnpricedModels      int64   `json:"unpriced_models"`
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
}

type ErrorTimelineReport struct {
	Timestamp     string `json:"timestamp"`
	Count         int64  `json:"count"`
	ParseErrors   int64  `json:"parse_errors"`
	DBErrors      int64  `json:"db_errors"`
	DroppedEvents int64  `json:"dropped_events"`
}

type HealthReport struct {
	Timestamp     string `json:"timestamp"`
	QueueDepth    int64  `json:"queue_depth"`
	DroppedEvents int64  `json:"dropped_events"`
	ParseErrors   int64  `json:"parse_errors"`
	DBErrors      int64  `json:"db_errors"`
	SSELineSkips  int64  `json:"sse_line_skips"`
}

type MetadataReport struct {
	Endpoints     []EndpointMeta `json:"endpoints"`
	Ranges        []RangeMeta    `json:"ranges"`
	Buckets       []BucketMeta   `json:"buckets"`
	MeteringKinds []string       `json:"metering_kinds"`
	CaptureModes  []string       `json:"capture_modes"`
}

type EndpointMeta struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	FilterValue  string `json:"filter_value"`
	Method       string `json:"method"`
	DisplayName  string `json:"display_name"`
	MeteringKind string `json:"metering_kind"`
	CaptureMode  string `json:"capture_mode"`
}

type RangeMeta struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Bucket string `json:"bucket"`
}

type BucketMeta struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type OverviewReport struct {
	Range    string          `json:"range"`
	Selected OverviewSection `json:"selected"`
	Recent1h OverviewSection `json:"recent_1h"`
	Capture  OverviewSection `json:"capture"`
	Cost     OverviewSection `json:"cost"`
}

type OverviewSection struct {
	Data  interface{} `json:"data"`
	Error string      `json:"error"`
}

type IssueReport struct {
	Class       string `json:"class"`
	Label       string `json:"label"`
	Count       int64  `json:"count"`
	Severity    string `json:"severity"`
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

type IssuesResponse struct {
	Range  string        `json:"range"`
	Total  int           `json:"total"`
	Items  []IssueReport `json:"items"`
	System IssuesSystem  `json:"system"`
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
