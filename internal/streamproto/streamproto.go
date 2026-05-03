package streamproto

// Protocol describes how a streaming endpoint formats its response data.
// This abstraction isolates stream parsing differences from the proxy main loop.
type Protocol struct {
	// Name identifies the protocol (e.g., "openai_sse", "none").
	Name string

	// UsesSSE indicates whether the stream uses Server-Sent Events framing.
	UsesSSE bool

	// EventBoundary is the byte sequence that separates events (typically '\n').
	EventBoundary byte

	// CompletionMarker, if non-empty, is a sentinel string that signals the stream
	// is complete (e.g., "[DONE]").
	CompletionMarker string

	// HasEventField indicates whether the protocol uses SSE event: lines that
	// should be parsed for routing.
	HasEventField bool

	// MaxLineSize is the maximum line length in bytes for parsing; longer lines
	// are forwarded but not parsed.
	MaxLineSize int
}

// OpenAISSE returns the OpenAI-style SSE protocol definition.
func OpenAISSE() Protocol {
	return Protocol{
		Name:             "openai_sse",
		UsesSSE:          true,
		EventBoundary:    '\n',
		CompletionMarker: "[DONE]",
		HasEventField:    false,
		MaxLineSize:      256 * 1024,
	}
}

// None returns a protocol definition for non-streaming endpoints.
func None() Protocol {
	return Protocol{
		Name: "none",
	}
}

// IsStreaming returns true if the protocol uses any streaming framing.
func (p Protocol) IsStreaming() bool {
	return p.UsesSSE
}
