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
	Model           string  `json:"model"`
	RequestCount    int64   `json:"request_count"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	ReasoningTokens int64   `json:"reasoning_tokens"`
	CachedTokens    int64   `json:"cached_tokens"`
	TotalTokens     int64   `json:"total_tokens"`
	Cost            float64 `json:"cost"`
	CostKnown       bool    `json:"cost_known"`
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
	Timestamp   string `json:"timestamp"`
	Count       int64  `json:"count"`
	InputTokens int64  `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens int64  `json:"total_tokens"`
}

type RequestReport struct {
	ID              int64  `json:"id"`
	CreatedAt       string `json:"created_at"`
	RequestID       string `json:"request_id"`
	Endpoint        string `json:"endpoint"`
	EndpointProfile string `json:"endpoint_profile"`
	CaptureMode     string `json:"capture_mode"`
	MeteringKind    string `json:"metering_kind"`
	Method          string `json:"method"`
	Status          int    `json:"status"`
	LatencyMs       int64  `json:"latency_ms"`
	TTFBMs          int64  `json:"ttfb_ms"`
	Stream          bool   `json:"stream"`
	ClientIPHash    string `json:"client_ip_hash"`
	APIKeyHash      string `json:"api_key_hash"`
	ModelRequested  string `json:"model_requested"`
	ModelReturned   string `json:"model_returned"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	RequestBytes    int64  `json:"request_bytes"`
	ResponseBytes   int64  `json:"response_bytes"`
	CaptureOutcome  string `json:"capture_outcome"`
	CaptureReason   string `json:"capture_reason"`
	Error           string `json:"error"`
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
