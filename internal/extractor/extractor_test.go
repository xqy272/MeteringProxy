package extractor

import (
	"encoding/json"
	"testing"
)

func TestExtractChatUsage_StreamingFinalChunk(t *testing.T) {
	input := []byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":361,"completion_tokens":56,"total_tokens":417,"completion_tokens_details":{"reasoning_tokens":11},"prompt_tokens_details":{"cached_tokens":0}}}`)

	u, err := ExtractChatUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 361 {
		t.Errorf("input_tokens = %d, want 361", u.InputTokens)
	}
	if u.OutputTokens != 56 {
		t.Errorf("output_tokens = %d, want 56", u.OutputTokens)
	}
	if u.TotalTokens != 417 {
		t.Errorf("total_tokens = %d, want 417", u.TotalTokens)
	}
	if u.ReasoningTokens != 11 {
		t.Errorf("reasoning_tokens = %d, want 11", u.ReasoningTokens)
	}
}

func TestExtractChatUsage_NilWhenNoUsage(t *testing.T) {
	input := []byte(`data: {"choices":[{"delta":{"content":"hello"}}]}`)
	u, err := ExtractChatUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Error("expected nil usage when no usage field")
	}
}

func TestExtractChatUsage_NilWhenDone(t *testing.T) {
	u, err := ExtractChatUsage([]byte("data: [DONE]"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Error("expected nil for [DONE]")
	}
}

func TestExtractChatUsage_RejectsResponsesFormat(t *testing.T) {
	// Responses API format with input_tokens/output_tokens but NOT prompt_tokens/completion_tokens
	input := []byte(`data: {"model":"gpt-5.4-mini","usage":{"input_tokens":371,"output_tokens":43,"total_tokens":414}}`)
	u, err := ExtractChatUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Error("expected nil; chat parser should reject responses-format usage to avoid zeroed tokens")
	}
}

func TestExtractResponsesUsage_CompletedEvent(t *testing.T) {
	input := []byte(`data: {"type":"response.completed","response":{"model":"gpt-5.4-mini-2026-03-17","usage":{"input_tokens":371,"output_tokens":43,"output_tokens_details":{"reasoning_tokens":0},"input_tokens_details":{"cached_tokens":0},"total_tokens":414}}}`)

	u, err := ExtractResponsesUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 371 {
		t.Errorf("input_tokens = %d, want 371", u.InputTokens)
	}
	if u.OutputTokens != 43 {
		t.Errorf("output_tokens = %d, want 43", u.OutputTokens)
	}
	if u.TotalTokens != 414 {
		t.Errorf("total_tokens = %d, want 414", u.TotalTokens)
	}
}

func TestExtractResponsesUsage_NilWhenNotCompleted(t *testing.T) {
	input := []byte(`data: {"type":"response.output_text.delta","delta":"hello"}`)
	u, err := ExtractResponsesUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Error("expected nil for non-completed event")
	}
}

func TestExtractNonStreaming_ChatFormat(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300,"prompt_tokens_details":{"cached_tokens":50}}}`)

	u, err := ExtractNonStreaming(body, "/v1/chat/completions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 100 {
		t.Errorf("input_tokens = %d, want 100", u.InputTokens)
	}
	if u.OutputTokens != 200 {
		t.Errorf("output_tokens = %d, want 200", u.OutputTokens)
	}
	if u.CachedTokens != 50 {
		t.Errorf("cached_tokens = %d, want 50", u.CachedTokens)
	}
}

func TestExtractNonStreaming_ResponsesFormat(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4-mini","usage":{"input_tokens":371,"output_tokens":43,"total_tokens":414,"input_tokens_details":{"cached_tokens":10}}}`)

	u, err := ExtractNonStreaming(body, "/v1/responses")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 371 {
		t.Errorf("input_tokens = %d, want 371", u.InputTokens)
	}
	if u.OutputTokens != 43 {
		t.Errorf("output_tokens = %d, want 43", u.OutputTokens)
	}
	if u.CachedTokens != 10 {
		t.Errorf("cached_tokens = %d, want 10", u.CachedTokens)
	}
}

func TestExtractNonStreaming_CorrectParserForEndpoint(t *testing.T) {
	// If a chat format response somehow reaches /v1/responses endpoint,
	// it should still detect and use chat format as fallback.
	chatBody := []byte(`{"model":"gpt-4o","usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300}}`)
	u, err := ExtractNonStreaming(chatBody, "/v1/responses")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil || u.InputTokens != 100 {
		t.Error("responses endpoint should fall back to chat format parser")
	}

	// Conversely, chat endpoint with responses body
	respBody := []byte(`{"model":"gpt-5.4-mini","usage":{"input_tokens":50,"output_tokens":60,"total_tokens":110}}`)
	u, err = ExtractNonStreaming(respBody, "/v1/chat/completions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil || u.InputTokens != 50 {
		t.Error("chat endpoint should fall back to responses format parser")
	}
}

func TestExtractNonStreaming_NoUsage(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","choices":[{"message":{"content":"hello"}}]}`)
	u, err := ExtractNonStreaming(body, "/v1/chat/completions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Error("expected nil when no usage in response")
	}
}

func TestStripSSEPrefix(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"data: {\"x\":1}", `{"x":1}`},
		{"data:data: {\"x\":1}", `{"x":1}`},
		{"  data: {\"x\":1}  ", `{"x":1}`},
		{"[DONE]", ""},
		{"data: [DONE]", ""},
		{"", ""},
	}
	for _, tc := range tests {
		got := stripSSEPrefix(tc.input)
		if got != tc.want {
			t.Errorf("stripSSEPrefix(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestExtractNonStreaming_ParseErrorOnTrash(t *testing.T) {
	// Complete garbage should return a parse error.
	body := []byte(`this is not json at all`)
	_, err := ExtractNonStreaming(body, "/v1/chat/completions")
	if err == nil {
		t.Error("expected parse error for non-JSON input")
	}
}

func TestExtractNonStreaming_NoErrorOnNoUsage(t *testing.T) {
	// Valid JSON without a usage field should return (nil, nil), not an error.
	body := []byte(`{"choices":[{"message":{"content":"hello"}}]}`)
	u, err := ExtractNonStreaming(body, "/v1/chat/completions")
	if err != nil {
		t.Errorf("unexpected error for valid JSON without usage: %v", err)
	}
	if u != nil {
		t.Error("expected nil usage")
	}
}

func TestTryChatFormat_ErrorOnTrash(t *testing.T) {
	_, err := tryChatFormat([]byte(`not json`))
	if err == nil {
		t.Error("expected parse error from tryChatFormat on non-JSON")
	}
}

func TestTryResponsesFormat_ErrorOnTrash(t *testing.T) {
	_, err := tryResponsesFormat([]byte(`not json`))
	if err == nil {
		t.Error("expected parse error from tryResponsesFormat on non-JSON")
	}
}

func TestUsageInfoJSONTags(t *testing.T) {
	u := UsageInfo{
		Model:           "gpt-4o",
		InputTokens:     100,
		OutputTokens:    200,
		ReasoningTokens: 10,
		CachedTokens:    5,
		TotalTokens:     300,
	}
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var u2 UsageInfo
	if err := json.Unmarshal(data, &u2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u2.InputTokens != 100 || u2.OutputTokens != 200 || u2.ReasoningTokens != 10 || u2.CachedTokens != 5 {
		t.Errorf("round-trip mismatch: %+v", u2)
	}
}
