package extractor

import (
	"strings"
	"testing"
)

func TestResponsesStreamStateParseErrorUsesStreamErrorTerminal(t *testing.T) {
	state := NewResponsesStreamState()
	state.ProcessSSEEvent([]byte(`data: {"type":"response.created","response":{"model":"gpt-test"}}`))
	state.ProcessSSEEvent([]byte(`data: {bad json}`))

	result := state.Result()
	if result.CaptureOutcome != "failed" || result.CaptureReason != "parse_error" {
		t.Fatalf("capture = %s/%s, want failed/parse_error", result.CaptureOutcome, result.CaptureReason)
	}
	if result.TerminalEvent != "stream_error" {
		t.Fatalf("TerminalEvent = %q, want stream_error", result.TerminalEvent)
	}
	if result.ModelReturnedSource != "" {
		t.Fatalf("ModelReturnedSource = %q, want empty", result.ModelReturnedSource)
	}
}

func TestResponsesStreamStateRejectsOversizedJSONEvent(t *testing.T) {
	state := NewResponsesStreamState()
	state.ProcessSSEEvent([]byte(`data: {"type":"` + strings.Repeat("x", maxResponsesSSEJSONBytes) + `"}`))
	if got := state.JSONParseErrors(); got != 1 {
		t.Fatalf("JSONParseErrors = %d, want 1", got)
	}
}

func TestResponsesStreamStateErrorEventPreservesErrorInfo(t *testing.T) {
	state := NewResponsesStreamState()
	state.ProcessSSEEvent([]byte(`data: {"type":"error","error":{"type":"invalid_request_error","code":"bad_param","param":"model"}}`))

	result := state.Result()
	if result.TerminalEvent != "error" {
		t.Fatalf("TerminalEvent = %q, want error", result.TerminalEvent)
	}
	if result.ErrorInfo == nil {
		t.Fatal("ErrorInfo = nil")
	}
	if result.ErrorInfo.Type != "invalid_request_error" || result.ErrorInfo.Code != "bad_param" || result.ErrorInfo.Param != "model" {
		t.Fatalf("ErrorInfo = %#v", result.ErrorInfo)
	}
}
