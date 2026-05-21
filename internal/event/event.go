package event

import "time"

// Event is the domain event for a metered request. It is independent of
// any database schema.
type Event struct {
	ID        string
	Timestamp time.Time

	EndpointProfile string
	CaptureMode     string
	MeteringKind    string

	Method    string
	Path      string
	Status    int
	Stream    bool
	LatencyMs int64
	TTFBMs    int64

	APIKeyHash   string
	ClientIPHash string

	ModelRequested string
	ModelReturned  string

	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CachedTokens        int64
	CacheCreationTokens int64
	TotalTokens         int64

	// Billable fields are reserved for future event-time pricing.
	// Currently cost is computed at query time in the WebUI;
	// these fields are always zero and stored as zero in the database.
	BillableInput  float64
	BillableOutput float64
	BillableTotal  float64
	BillableUnit   string

	UsageRawJSON      string
	UsageRawTruncated bool

	CaptureOutcome string
	CaptureReason  string

	ErrorClass            string
	ErrorType             string
	ErrorCode             string
	ErrorParam            string
	ErrorMessage          string
	ErrorMessageTruncated bool

	RequestBytes  int64
	ResponseBytes int64
	Error         string

	ModelReturnedSource string
	UsageSource         string
	TerminalEvent       string
	TerminalReason      string
	SideUsageEventID    int64

	UsageDimensions []UsageDimension
	ImageUsage      *ImageUsage
}

// UsageDimension is a normalized usage metric attached to a request.
type UsageDimension struct {
	EndpointProfile string
	Provider        string
	Model           string
	Modality        string
	Channel         string
	Metric          string
	Direction       string
	Unit            string
	Amount          float64
	UsageSource     string
	CaptureOutcome  string
	CaptureReason   string
	DetailsJSON     string
}

// ImageUsage stores image-specific request metadata and aggregate usage.
type ImageUsage struct {
	Operation         string
	Provider          string
	ModelRequested    string
	ModelReturned     string
	Size              string
	Quality           string
	OutputFormat      string
	Stream            bool
	ImageCount        int64
	PartialImageCount int64
	InputImageCount   int64
	HasMask           bool
	UsageSource       string
	CaptureOutcome    string
	CaptureReason     string
	MetadataJSON      string
}

// Capture mode constants.
const (
	CapturePassthrough  = "passthrough"
	CaptureRequestOnly  = "request_only"
	CaptureUsageMetered = "usage_metered"
)

// Metering kind constants.
const (
	MeteringNone            = "none"
	MeteringLLMTokens       = "llm_tokens"
	MeteringEmbeddingTokens = "embedding_tokens"
	MeteringAudioSeconds    = "audio_seconds"
	MeteringImageCount      = "image_count"
	MeteringImageTokens     = "image_tokens"
	MeteringRequestOnly     = "request_only"
	MeteringUnknown         = "unknown"
)

// Capture outcome constants.
const (
	OutcomeCaptured = "captured"
	OutcomeSkipped  = "skipped"
	OutcomeFailed   = "failed"
)

// Capture reason constants.
const (
	ReasonCaptureDisabled               = "capture_disabled"
	ReasonProfilePassthrough            = "profile_passthrough"
	ReasonRequestOnlyProfile            = "request_only_profile"
	ReasonUsageNotPresent               = "usage_not_present"
	ReasonSampleLimitExceeded           = "sample_limit_exceeded"
	ReasonStreamProtocolUnsupported     = "stream_protocol_unsupported"
	ReasonParseError                    = "parse_error"
	ReasonWriterQueueFull               = "writer_queue_full"
	ReasonUpstreamError                 = "upstream_error"
	ReasonResponseCompletedWithoutUsage = "response_completed_without_usage"
	ReasonResponseIncomplete            = "response_incomplete"
	ReasonStreamEndedWithoutCompleted   = "stream_ended_without_completed"
	ReasonResponseErrorEvent            = "response_error_event"
)

// Model returned source constants.
const (
	SourceUsage              = "usage"
	SourceResponseCompleted  = "response_completed"
	SourceResponseCreated    = "response_created"
	SourceResponseFailed     = "response_failed"
	SourceResponseIncomplete = "response_incomplete"
	SourceHTTPResponse       = "http_response"
	SourceSideChannel        = "side_channel"
)

// Usage source constants.
const (
	UsageSourceHTTPResponse = "http_response"
	UsageSourceCliproxySide = "cliproxy_side_channel"
)

// Terminal event constants.
const (
	TerminalResponseCompleted  = "response.completed"
	TerminalResponseFailed     = "response.failed"
	TerminalResponseIncomplete = "response.incomplete"
	TerminalStreamEnd          = "stream_end"
	TerminalStreamError        = "stream_error"
	TerminalChatFinish         = "chat.finish"
	TerminalMessageStop        = "message_stop"
	TerminalGeminiFinish       = "gemini.finish"
)
