package profile

import (
	"net/http"
	"testing"

	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/extractor"
)

func TestRegistry_ChatCompletionsMatch(t *testing.T) {
	r := NewRegistry()
	for _, path := range []string{
		"/v1/chat/completions",
		"/api/provider/openai/chat/completions",
		"/api/provider/openai/v1/chat/completions",
	} {
		p, err := r.Match(http.MethodPost, path)
		if err != nil {
			t.Fatalf("Match(%q): %v", path, err)
		}
		if p.Name != "chat_completions" {
			t.Errorf("profile name for %q = %q, want chat_completions", path, p.Name)
		}
		if p.CaptureMode != event.CaptureUsageMetered {
			t.Errorf("capture_mode = %q, want %q", p.CaptureMode, event.CaptureUsageMetered)
		}
		if p.MeteringKind != event.MeteringLLMTokens {
			t.Errorf("metering_kind = %q, want %q", p.MeteringKind, event.MeteringLLMTokens)
		}
		if !p.StreamProtocol.UsesSSE {
			t.Error("chat completions should use SSE")
		}
		if p.StreamProtocol.CompletionMarker != "[DONE]" {
			t.Error("chat completions should use [DONE] completion marker")
		}
		if !p.IsMetered() {
			t.Error("chat completions should be metered")
		}
	}
}

func TestRegistry_OpenAICompletionsMatch(t *testing.T) {
	r := NewRegistry()
	for _, path := range []string{
		"/v1/completions",
		"/api/provider/openai/completions",
		"/api/provider/openai/v1/completions",
	} {
		p, err := r.Match(http.MethodPost, path)
		if err != nil {
			t.Fatalf("Match(%q): %v", path, err)
		}
		if p.Name != "openai_completions" {
			t.Errorf("profile name for %q = %q, want openai_completions", path, p.Name)
		}
		if p.CaptureMode != event.CaptureUsageMetered {
			t.Errorf("capture_mode = %q, want %q", p.CaptureMode, event.CaptureUsageMetered)
		}
	}
}

func TestRegistry_ResponsesMatch(t *testing.T) {
	r := NewRegistry()
	for _, path := range []string{
		"/v1/responses",
		"/v1/responses/compact",
		"/backend-api/codex/responses",
		"/backend-api/codex/responses/compact",
		"/api/provider/openai/responses",
		"/api/provider/openai/v1/responses",
	} {
		p, err := r.Match(http.MethodPost, path)
		if err != nil {
			t.Fatalf("Match(%q): %v", path, err)
		}
		if p.Name != "responses" {
			t.Errorf("profile name for %q = %q, want responses", path, p.Name)
		}
		if p.CaptureMode != event.CaptureUsageMetered {
			t.Errorf("capture_mode = %q, want %q", p.CaptureMode, event.CaptureUsageMetered)
		}
		if p.MeteringKind != event.MeteringLLMTokens {
			t.Errorf("metering_kind = %q, want %q", p.MeteringKind, event.MeteringLLMTokens)
		}
	}
}

func TestRegistry_AnthropicMessagesMatch(t *testing.T) {
	r := NewRegistry()
	for _, path := range []string{"/v1/messages", "/api/provider/anthropic/v1/messages"} {
		p, err := r.Match(http.MethodPost, path)
		if err != nil {
			t.Fatalf("Match(%q): %v", path, err)
		}
		if p.Name != "anthropic_messages" {
			t.Errorf("profile name for %q = %q, want anthropic_messages", path, p.Name)
		}
		if p.CaptureMode != event.CaptureUsageMetered {
			t.Errorf("capture_mode = %q, want %q", p.CaptureMode, event.CaptureUsageMetered)
		}
		if p.MeteringKind != event.MeteringLLMTokens {
			t.Errorf("metering_kind = %q, want %q", p.MeteringKind, event.MeteringLLMTokens)
		}
		if !p.IsMetered() {
			t.Error("Anthropic Messages should be metered")
		}
	}
}

func TestRegistry_GeminiGenerateContentMatch(t *testing.T) {
	r := NewRegistry()
	for _, path := range []string{
		"/v1beta/models/gemini-2.5-pro:generateContent",
		"/v1beta/models/gemini-2.5-pro:streamGenerateContent",
		"/v1/models/gemini-2.5-flash:generateContent",
		"/api/provider/google/v1beta/models/gemini-2.5-pro:generateContent",
		"/api/provider/google/v1/models/gemini-2.5-flash:streamGenerateContent",
	} {
		p, err := r.Match(http.MethodPost, path)
		if err != nil {
			t.Fatalf("Match(%q): %v", path, err)
		}
		if p.Name != "gemini_generate_content" {
			t.Errorf("profile name for %q = %q, want gemini_generate_content", path, p.Name)
		}
		if p.CaptureMode != event.CaptureUsageMetered {
			t.Errorf("capture_mode = %q, want %q", p.CaptureMode, event.CaptureUsageMetered)
		}
	}

	p, err := r.Match(http.MethodPost, "/v1beta/models/gemini-2.5-pro:countTokens")
	if err != nil {
		t.Fatalf("Match countTokens: %v", err)
	}
	if p.Name != "unknown_passthrough" {
		t.Errorf("countTokens matched %s, want unknown_passthrough", p.Name)
	}

	for _, path := range []string{
		"/v1beta/models/gemini-2.5-pro:generateContent/extra",
		"/v1beta/models/gemini-2.5-pro:streamGenerateContent/extra",
		"/v1beta/models/gemini-2.5-pro:generateContentExtra",
	} {
		p, err := r.Match(http.MethodPost, path)
		if err != nil {
			t.Fatalf("Match(%q): %v", path, err)
		}
		if p.Name != "unknown_passthrough" {
			t.Errorf("%q matched %s, want unknown_passthrough", path, p.Name)
		}
	}
}

func TestRegistry_UnknownPassthrough(t *testing.T) {
	r := NewRegistry()

	// GET /v1/models should match passthrough
	p, err := r.Match(http.MethodGet, "/v1/models")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if p.Name != "unknown_passthrough" {
		t.Errorf("profile name = %q, want unknown_passthrough", p.Name)
	}
	if p.CaptureMode != event.CapturePassthrough {
		t.Errorf("capture_mode = %q, want %q", p.CaptureMode, event.CapturePassthrough)
	}
	if p.IsMetered() {
		t.Error("passthrough should not be metered")
	}

	// POST to unknown path should also match passthrough
	p, err = r.Match(http.MethodPost, "/v1/embeddings")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if p.Name != "unknown_passthrough" {
		t.Errorf("unexpected profile: %s", p.Name)
	}
}

func TestRegistry_ChatCompletionsDoesNotMatchWrongMethod(t *testing.T) {
	r := NewRegistry()
	p, err := r.Match(http.MethodGet, "/v1/chat/completions")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	// Should fall through to passthrough since GET != POST
	if p.Name != "unknown_passthrough" {
		t.Errorf("GET /v1/chat/completions matched %s, want unknown_passthrough", p.Name)
	}
}

func TestRegistry_ChatCompletionsDoesNotMatchSubpath(t *testing.T) {
	r := NewRegistry()
	p, err := r.Match(http.MethodPost, "/v1/chat/completions/extra")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if p.Name != "unknown_passthrough" {
		t.Errorf("POST /v1/chat/completions/extra matched %s, want unknown_passthrough", p.Name)
	}
}

func TestRegistry_TrailingSlashNormalization(t *testing.T) {
	r := NewRegistry()
	p, err := r.Match(http.MethodPost, "/v1/chat/completions/")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if p.Name != "chat_completions" {
		t.Errorf("POST /v1/chat/completions/ matched %s, want chat_completions", p.Name)
	}
}

func TestRegistry_ExtractorBinding_ChatCompletions(t *testing.T) {
	r := NewRegistry()
	p, err := r.Match(http.MethodPost, "/v1/chat/completions")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}

	if p.NonStreamExtractor == nil {
		t.Fatal("chat completions non-stream extractor is nil")
	}
	if p.StreamExtractor == nil {
		t.Fatal("chat completions stream extractor is nil")
	}

	// Verify stream extractor works for chat format
	u, err := p.StreamExtractor([]byte(`data: {"model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	if err != nil {
		t.Fatalf("stream extractor error: %v", err)
	}
	if u == nil || u.InputTokens != 10 {
		t.Error("stream extractor failed to parse chat usage")
	}

	// Verify non-stream extractor works
	u, err = p.NonStreamExtractor([]byte(`{"model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`), "/v1/chat/completions")
	if err != nil {
		t.Fatalf("non-stream extractor error: %v", err)
	}
	if u == nil || u.InputTokens != 10 {
		t.Error("non-stream extractor failed to parse chat usage")
	}
}

func TestRegistry_ExtractorBinding_Responses(t *testing.T) {
	r := NewRegistry()
	p, err := r.Match(http.MethodPost, "/v1/responses")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}

	if p.StreamExtractor == nil {
		t.Fatal("responses stream extractor is nil")
	}

	// Verify responses stream extractor
	u, err := p.StreamExtractor([]byte(`data: {"type":"response.completed","response":{"model":"gpt-5.4-mini","usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30}}}`))
	if err != nil {
		t.Fatalf("responses stream extractor error: %v", err)
	}
	if u == nil || u.InputTokens != 20 {
		t.Error("responses stream extractor failed")
	}

	// Verify it rejects non-completed events
	u, err = p.StreamExtractor([]byte(`data: {"type":"response.output_text.delta","delta":"hello"}`))
	if err != nil {
		t.Fatalf("error on non-completed event: %v", err)
	}
	if u != nil {
		t.Error("expected nil usage for non-completed event")
	}
}

func TestRegistry_ExtractorBinding_AnthropicMessages(t *testing.T) {
	r := NewRegistry()
	p, err := r.Match(http.MethodPost, "/v1/messages")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if p.NonStreamExtractor == nil || p.StreamExtractor == nil {
		t.Fatal("Anthropic extractors should be bound")
	}
	u, err := p.NonStreamExtractor([]byte(`{"model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":5}}`), "/v1/messages")
	if err != nil {
		t.Fatalf("non-stream extractor error: %v", err)
	}
	if u == nil || u.InputTokens != 10 || u.OutputTokens != 5 {
		t.Fatalf("Anthropic non-stream extractor failed: %+v", u)
	}
	u, err = p.StreamExtractor([]byte(`data: {"type":"message_delta","usage":{"output_tokens":12}}`))
	if err != nil {
		t.Fatalf("stream extractor error: %v", err)
	}
	if u == nil || u.OutputTokens != 12 {
		t.Fatalf("Anthropic stream extractor failed: %+v", u)
	}
}

func TestRegistry_ExtractorBinding_GeminiGenerateContent(t *testing.T) {
	r := NewRegistry()
	p, err := r.Match(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if p.NonStreamExtractor == nil || p.StreamExtractor == nil {
		t.Fatal("Gemini extractors should be bound")
	}
	u, err := p.NonStreamExtractor([]byte(`{"modelVersion":"gemini-2.5-flash","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`), "/v1beta/models/gemini-2.5-flash:generateContent")
	if err != nil {
		t.Fatalf("non-stream extractor error: %v", err)
	}
	if u == nil || u.InputTokens != 10 || u.OutputTokens != 5 {
		t.Fatalf("Gemini non-stream extractor failed: %+v", u)
	}
	u, err = p.StreamExtractor([]byte(`data: {"modelVersion":"gemini-2.5-flash","usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":6,"totalTokenCount":17}}`))
	if err != nil {
		t.Fatalf("stream extractor error: %v", err)
	}
	if u == nil || u.InputTokens != 11 || u.OutputTokens != 6 {
		t.Fatalf("Gemini stream extractor failed: %+v", u)
	}
}

func TestRegistry_Profiles(t *testing.T) {
	r := NewRegistry()
	all := r.Profiles()
	if len(all) != 6 {
		t.Errorf("expected 6 profiles, got %d", len(all))
	}
	metered := r.MeteredProfiles()
	if len(metered) != 5 {
		t.Errorf("expected 5 metered profiles, got %d", len(metered))
	}
}

func TestEndpointProfile_DisplayName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"chat_completions", "Chat Completions"},
		{"openai_completions", "Completions"},
		{"responses", "Responses API"},
		{"anthropic_messages", "Anthropic Messages"},
		{"gemini_generate_content", "Gemini Generate Content"},
		{"unknown_passthrough", "Unknown (Passthrough)"},
		{"custom", "custom"},
	}
	for _, tc := range tests {
		p := &EndpointProfile{Name: tc.name}
		if got := p.DisplayName(); got != tc.want {
			t.Errorf("DisplayName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEndpointProfile_ToEndpointMeta(t *testing.T) {
	p := &EndpointProfile{
		Name:         "chat_completions",
		PathPrefix:   "/v1/chat/completions",
		Method:       "POST",
		CaptureMode:  event.CaptureUsageMetered,
		MeteringKind: event.MeteringLLMTokens,
	}
	meta := p.ToEndpointMeta()
	if meta.Name != "chat_completions" {
		t.Errorf("meta.Name = %q", meta.Name)
	}
	if meta.Path != "/v1/chat/completions" {
		t.Errorf("meta.Path = %q", meta.Path)
	}
	if meta.FilterValue != "/v1/chat/completions" {
		t.Errorf("meta.FilterValue = %q", meta.FilterValue)
	}
	if meta.DisplayName != "Chat Completions" {
		t.Errorf("meta.DisplayName = %q", meta.DisplayName)
	}
	if meta.MeteringKind != event.MeteringLLMTokens {
		t.Errorf("meta.MeteringKind = %q", meta.MeteringKind)
	}

	dynamic := &EndpointProfile{
		Name:        "gemini_generate_content",
		PathPrefix:  "/v1(beta)?/models/{model}:generateContent|streamGenerateContent",
		Method:      "POST",
		PathMatcher: func(path string) bool { return true },
	}
	meta = dynamic.ToEndpointMeta()
	if meta.FilterValue != "profile:gemini_generate_content" {
		t.Errorf("dynamic meta.FilterValue = %q, want profile:gemini_generate_content", meta.FilterValue)
	}
}

// Ensure the embedded extractor functions return concrete types.
var _ = extractor.UsageInfo{}
