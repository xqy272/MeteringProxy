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

	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	CachedTokens    int64
	TotalTokens     int64

	// Billable fields are reserved for future event-time pricing.
	// Currently cost is computed at query time via pricing.Cost() in the WebUI;
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
	ReasonCaptureDisabled           = "capture_disabled"
	ReasonProfilePassthrough        = "profile_passthrough"
	ReasonRequestOnlyProfile        = "request_only_profile"
	ReasonUsageNotPresent           = "usage_not_present"
	ReasonSampleLimitExceeded       = "sample_limit_exceeded"
	ReasonStreamProtocolUnsupported = "stream_protocol_unsupported"
	ReasonParseError                = "parse_error"
	ReasonWriterQueueFull           = "writer_queue_full"
	ReasonUpstreamError             = "upstream_error"
)
