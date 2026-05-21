package extractor

import (
	"encoding/json"
	"strings"
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
	if u.UsageRawJSON == "" || !strings.Contains(u.UsageRawJSON, `"prompt_tokens":361`) {
		t.Errorf("UsageRawJSON = %q, want compact usage subset", u.UsageRawJSON)
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

func TestExtractResponsesUsage_ImageGenerationCompletedEvent(t *testing.T) {
	input := []byte(`data: {"type":"response.completed","response":{"model":"gpt-5.4-mini","output":[{"type":"image_generation_call","status":"completed","result":"private-image-bytes"}],"usage":{"input_tokens":371,"output_tokens":43,"total_tokens":414}}}`)

	u, err := ExtractResponsesUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.ImageCount != 1 || u.Operation != "generation" || u.InputTokens != 371 || u.OutputTokens != 43 {
		t.Fatalf("responses image usage = %+v", u)
	}
	if strings.Contains(u.UsageRawJSON, "private-image-bytes") {
		t.Fatalf("usage raw JSON leaked image result: %q", u.UsageRawJSON)
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

func TestExtractAnthropicUsage_MessageStart(t *testing.T) {
	input := []byte(`data: {"type":"message_start","message":{"model":"claude-sonnet-4-6-20250514","usage":{"input_tokens":100,"cache_creation_input_tokens":5,"cache_read_input_tokens":20,"output_tokens":1}}}`)
	u, err := ExtractAnthropicUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.Model != "claude-sonnet-4-6-20250514" {
		t.Errorf("model = %q", u.Model)
	}
	if u.InputTokens != 125 {
		t.Errorf("input_tokens = %d, want 125", u.InputTokens)
	}
	if u.OutputTokens != 1 {
		t.Errorf("output_tokens = %d, want 1", u.OutputTokens)
	}
	if u.CachedTokens != 20 {
		t.Errorf("cached_tokens = %d, want 20", u.CachedTokens)
	}
	if u.CacheCreationTokens != 5 {
		t.Errorf("cache_creation_tokens = %d, want 5", u.CacheCreationTokens)
	}
	if u.TotalTokens != 126 {
		t.Errorf("total_tokens = %d, want 126", u.TotalTokens)
	}
}

func TestExtractAnthropicUsage_MessageDeltaPartial(t *testing.T) {
	input := []byte(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":45}}`)
	u, err := ExtractAnthropicUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 0 {
		t.Errorf("input_tokens = %d, want 0 for partial delta", u.InputTokens)
	}
	if u.OutputTokens != 45 {
		t.Errorf("output_tokens = %d, want 45", u.OutputTokens)
	}
	if u.TotalTokens != 45 {
		t.Errorf("total_tokens = %d, want 45 for output-only partial delta", u.TotalTokens)
	}
}

func TestExtractAnthropicUsage_NegativeAndOverflowTokensAreClamped(t *testing.T) {
	input := []byte(`data: {"type":"message_start","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":9223372036854775807,"cache_creation_input_tokens":5,"cache_read_input_tokens":-20,"output_tokens":1}}}`)
	u, err := ExtractAnthropicUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != maxTokenCount {
		t.Errorf("input_tokens = %d, want saturated maxTokenCount", u.InputTokens)
	}
	if u.CachedTokens != 0 {
		t.Errorf("cached_tokens = %d, want 0 for negative provider value", u.CachedTokens)
	}
	if u.TotalTokens != maxTokenCount {
		t.Errorf("total_tokens = %d, want saturated maxTokenCount", u.TotalTokens)
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

func TestExtractNonStreaming_ResponsesImageGenerationSample(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4-mini","output":[{"type":"image_generation_call","result":"` + strings.Repeat("x", 4096) + `"}],"usage":{"input_tokens":371,"output_tokens":43,"total_tokens":414}}`)
	sample := []byte(string(body[:128]) + "\n" + string(body[len(body)-128:]))
	u, err := ExtractNonStreaming(sample, "/v1/responses")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.ImageCount != 1 || u.Operation != "generation" || u.InputTokens != 371 || u.OutputTokens != 43 {
		t.Fatalf("responses image sample = %+v", u)
	}
	if strings.Contains(u.UsageRawJSON, "xxx") {
		t.Fatalf("usage raw JSON leaked sampled image result: %q", u.UsageRawJSON)
	}
}

func TestExtractAnthropicNonStreaming(t *testing.T) {
	body := []byte(`{"id":"msg_01","type":"message","model":"claude-sonnet-4-6-20250514","usage":{"input_tokens":100,"cache_creation_input_tokens":5,"cache_read_input_tokens":20,"output_tokens":30}}`)

	u, err := ExtractAnthropicNonStreaming(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.Model != "claude-sonnet-4-6-20250514" {
		t.Errorf("model = %q", u.Model)
	}
	if u.InputTokens != 125 {
		t.Errorf("input_tokens = %d, want 125", u.InputTokens)
	}
	if u.OutputTokens != 30 {
		t.Errorf("output_tokens = %d, want 30", u.OutputTokens)
	}
	if u.CachedTokens != 20 {
		t.Errorf("cached_tokens = %d, want 20", u.CachedTokens)
	}
	if u.CacheCreationTokens != 5 {
		t.Errorf("cache_creation_tokens = %d, want 5", u.CacheCreationTokens)
	}
	if u.TotalTokens != 155 {
		t.Errorf("total_tokens = %d, want 155", u.TotalTokens)
	}
	if !strings.Contains(u.UsageRawJSON, `"cache_read_input_tokens":20`) {
		t.Errorf("UsageRawJSON = %q, want Anthropic usage subset", u.UsageRawJSON)
	}
}

func TestExtractGeminiNonStreaming(t *testing.T) {
	body := []byte(`{"modelVersion":"gemini-2.5-pro","usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"toolUsePromptTokenCount":5,"candidatesTokenCount":30,"thoughtsTokenCount":10,"totalTokenCount":145}}`)

	u, err := ExtractGeminiNonStreaming(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.Model != "gemini-2.5-pro" {
		t.Errorf("model = %q", u.Model)
	}
	if u.InputTokens != 105 {
		t.Errorf("input_tokens = %d, want 105", u.InputTokens)
	}
	if u.OutputTokens != 40 {
		t.Errorf("output_tokens = %d, want 40", u.OutputTokens)
	}
	if u.ReasoningTokens != 10 {
		t.Errorf("reasoning_tokens = %d, want 10", u.ReasoningTokens)
	}
	if u.CachedTokens != 20 {
		t.Errorf("cached_tokens = %d, want 20", u.CachedTokens)
	}
	if u.TotalTokens != 145 {
		t.Errorf("total_tokens = %d, want 145", u.TotalTokens)
	}
}

func TestExtractGeminiNonStreaming_KeepsProviderTotal(t *testing.T) {
	body := []byte(`{"modelVersion":"gemini-2.5-pro","usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":5,"candidatesTokenCount":30,"thoughtsTokenCount":10,"totalTokenCount":140}}`)

	u, err := ExtractGeminiNonStreaming(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 105 {
		t.Errorf("input_tokens = %d, want 105", u.InputTokens)
	}
	if u.OutputTokens != 40 {
		t.Errorf("output_tokens = %d, want 40", u.OutputTokens)
	}
	if u.TotalTokens != 140 {
		t.Errorf("total_tokens = %d, want provider total 140", u.TotalTokens)
	}
}

func TestExtractGeminiNonStreaming_FallbackToCachedWhenInputZero(t *testing.T) {
	body := []byte(`{"modelVersion":"gemini-2.5-pro","usageMetadata":{"promptTokenCount":0,"cachedContentTokenCount":50,"candidatesTokenCount":30,"totalTokenCount":80}}`)

	u, err := ExtractGeminiNonStreaming(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 50 {
		t.Errorf("input_tokens = %d, want 50 (fallback to cachedContentTokenCount)", u.InputTokens)
	}
}

func TestExtractGeminiNonStreaming_DoesNotReplaceInputWithCached(t *testing.T) {
	body := []byte(`{"modelVersion":"gemini-2.5-pro","usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":50,"candidatesTokenCount":30,"totalTokenCount":80}}`)

	u, err := ExtractGeminiNonStreaming(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 10 {
		t.Errorf("input_tokens = %d, want 10 (cachedContentTokenCount should not replace promptTokenCount)", u.InputTokens)
	}
	if u.CachedTokens != 50 {
		t.Errorf("cached_tokens = %d, want 50", u.CachedTokens)
	}
}

func TestExtractGeminiUsage_StreamingChunk(t *testing.T) {
	input := []byte(`data: {"modelVersion":"gemini-2.5-flash","usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":12,"totalTokenCount":62}}`)
	u, err := ExtractGeminiUsage(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 50 || u.OutputTokens != 12 || u.TotalTokens != 62 {
		t.Errorf("usage = %+v, want 50/12/62", u)
	}
}

func TestExtractGeminiNonStreaming_ImageOutput(t *testing.T) {
	body := []byte(`{"modelVersion":"gemini-3.1-flash-image","candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"private-image-bytes"}}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":30,"totalTokenCount":50}}`)
	u, err := ExtractGeminiNonStreaming(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.Model != "gemini-3.1-flash-image" || u.ImageCount != 1 || u.Operation != "generation" {
		t.Fatalf("image usage = %+v, want model image count", u)
	}
	if strings.Contains(u.UsageRawJSON, "private-image-bytes") {
		t.Fatalf("UsageRawJSON leaked image bytes: %q", u.UsageRawJSON)
	}
}

func TestExtractGeminiNonStreaming_PrefixTailImageSample(t *testing.T) {
	sample := []byte(`{"modelVersion":"gemini-3.1-flash-image","candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + "\n" + `"}}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":30,"totalTokenCount":50}}`)
	u, err := ExtractGeminiNonStreaming(sample)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage from sampled response")
	}
	if u.Model != "gemini-3.1-flash-image" || u.ImageCount != 1 || u.InputTokens != 20 || u.OutputTokens != 30 || u.TotalTokens != 50 {
		t.Fatalf("sample usage = %+v", u)
	}
}

func TestExtractImageNonStreaming(t *testing.T) {
	body := []byte(`{"created":1713833628,"data":[{"b64_json":"..."}],"usage":{"total_tokens":100,"input_tokens":50,"output_tokens":50,"input_tokens_details":{"text_tokens":10,"image_tokens":40}}}`)
	u, err := ExtractImageNonStreaming(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 50 || u.OutputTokens != 50 || u.TotalTokens != 100 {
		t.Fatalf("tokens = %+v, want 50/50/100", u)
	}
	if u.InputTextTokens != 10 || u.InputImageTokens != 40 || u.ImageCount != 1 {
		t.Fatalf("image details = %+v, want text=10 image=40 count=1", u)
	}
}

func TestExtractImageNonStreaming_PrefixTailSample(t *testing.T) {
	sample := []byte(`{"model":"gpt-image-2","data":[{"b64_json":"` + "\n" + `"}],"usage":{"total_tokens":100,"input_tokens":50,"output_tokens":50,"input_tokens_details":{"text_tokens":10,"image_tokens":40}}}`)
	u, err := ExtractImageNonStreaming(sample)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage from sampled response")
	}
	if u.Model != "gpt-image-2" || u.InputTextTokens != 10 || u.InputImageTokens != 40 || u.OutputTokens != 50 || u.ImageCount != 1 {
		t.Fatalf("sample usage = %+v", u)
	}
}

func TestExtractImageUsage_StreamingEvents(t *testing.T) {
	partial, err := ExtractImageUsage([]byte(`data: {"type":"image_generation.partial_image","partial_image_index":0,"b64_json":"..."}`))
	if err != nil {
		t.Fatalf("partial image event error: %v", err)
	}
	if partial == nil || partial.PartialImageCount != 1 {
		t.Fatalf("partial usage = %+v, want one partial image", partial)
	}
	done, err := ExtractImageUsage([]byte(`data: {"type":"image_generation.completed","b64_json":"...","usage":{"total_tokens":100,"input_tokens":50,"output_tokens":50,"input_tokens_details":{"text_tokens":10,"image_tokens":40}}}`))
	if err != nil {
		t.Fatalf("completed image event error: %v", err)
	}
	if done == nil || done.ImageCount != 1 || done.InputTextTokens != 10 || done.InputImageTokens != 40 || done.OutputTokens != 50 {
		t.Fatalf("completed usage = %+v", done)
	}
}

func TestExtractGeminiNonStreaming_ArrayUsesLastUsage(t *testing.T) {
	body := []byte(`[{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]},{"modelVersion":"gemini-2.5-flash","usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":5,"totalTokenCount":13}}]`)
	u, err := ExtractGeminiNonStreaming(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.InputTokens != 8 || u.OutputTokens != 5 || u.TotalTokens != 13 {
		t.Errorf("usage = %+v, want 8/5/13", u)
	}
}

func TestExtractGeminiNonStreaming_ArrayKeepsImageOutputFromEarlierChunk(t *testing.T) {
	body := []byte(`[{"modelVersion":"gemini-3.1-flash-image","candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"private-image-bytes"}}]}}]},{"modelVersion":"gemini-3.1-flash-image","usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":5,"totalTokenCount":13}}]`)
	u, err := ExtractGeminiNonStreaming(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil usage")
	}
	if u.ImageCount != 1 || u.Operation != "generation" || u.InputTokens != 8 || u.OutputTokens != 5 {
		t.Fatalf("usage = %+v, want image count and final usage", u)
	}
	if strings.Contains(u.UsageRawJSON, "private-image-bytes") {
		t.Fatalf("UsageRawJSON leaked image bytes: %q", u.UsageRawJSON)
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

func TestExtractNonStreaming_ChatZeroTokenUsageIsNil(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","usage":{"total_tokens":0}}`)
	u, err := ExtractNonStreaming(body, "/v1/chat/completions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Fatalf("expected nil usage for all-zero chat usage, got %+v", u)
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
		Model:               "gpt-4o",
		InputTokens:         100,
		OutputTokens:        200,
		ReasoningTokens:     10,
		CachedTokens:        5,
		CacheCreationTokens: 3,
		TotalTokens:         300,
	}
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var u2 UsageInfo
	if err := json.Unmarshal(data, &u2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u2.InputTokens != 100 || u2.OutputTokens != 200 || u2.ReasoningTokens != 10 || u2.CachedTokens != 5 || u2.CacheCreationTokens != 3 {
		t.Errorf("round-trip mismatch: %+v", u2)
	}
}
