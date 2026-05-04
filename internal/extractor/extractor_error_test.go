package extractor

import (
	"strings"
	"testing"
)

func TestExtractErrorInfo_OpenAIQuotaExhausted(t *testing.T) {
	body := []byte(`{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","code":"insufficient_quota","param":null}}`)
	info, err := ExtractErrorInfo(body, 429, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Class != "quota_exhausted" {
		t.Errorf("class = %q, want quota_exhausted", info.Class)
	}
	if info.Type != "insufficient_quota" {
		t.Errorf("type = %q, want insufficient_quota", info.Type)
	}
	if info.Code != "insufficient_quota" {
		t.Errorf("code = %q, want insufficient_quota", info.Code)
	}
	if info.Message == "" {
		t.Error("message should not be empty")
	}
}

func TestExtractErrorInfo_OpenAIRateLimit(t *testing.T) {
	body := []byte(`{"error":{"message":"Rate limit reached for requests","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
	info, err := ExtractErrorInfo(body, 429, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Class != "rate_limited" {
		t.Errorf("class = %q, want rate_limited", info.Class)
	}
}

func TestExtractErrorInfo_OpenAIAuthFailed(t *testing.T) {
	body := []byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`)
	info, err := ExtractErrorInfo(body, 401, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Class != "auth_failed" {
		t.Errorf("class = %q, want auth_failed", info.Class)
	}
}

func TestExtractErrorInfo_AnthropicError(t *testing.T) {
	body := []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	info, err := ExtractErrorInfo(body, 529, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Class == "" {
		t.Error("class should not be empty")
	}
}

func TestExtractErrorInfo_GenericMessageCodeType(t *testing.T) {
	body := []byte(`{"message":"Not found","code":"not_found","type":"invalid_request"}`)
	info, err := ExtractErrorInfo(body, 404, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Message != "Not found" {
		t.Errorf("message = %q, want 'Not found'", info.Message)
	}
}

func TestExtractErrorInfo_NonJSONBody(t *testing.T) {
	info, err := ExtractErrorInfo([]byte("plain text error"), 500, "text/plain")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for non-JSON body")
	}
}

func TestExtractErrorInfo_EmptyBody(t *testing.T) {
	info, err := ExtractErrorInfo([]byte(""), 500, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for empty body")
	}
}

func TestExtractErrorInfo_HTMLBody(t *testing.T) {
	html := []byte("<html><body><h1>502 Bad Gateway</h1></body></html>")
	info, err := ExtractErrorInfo(html, 502, "text/html")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for HTML body")
	}
}

func TestExtractErrorInfo_LongMessageTruncation(t *testing.T) {
	longMsg := strings.Repeat("a", 1000)
	body := []byte("{\"error\":{\"message\":\"" + longMsg + "\",\"type\":\"test\"}}")
	info, err := ExtractErrorInfo(body, 500, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if len(info.Message) > 500 {
		t.Errorf("message length = %d, want <= 500", len(info.Message))
	}
	if !info.MessageTruncated {
		t.Error("MessageTruncated should be true")
	}
}

func TestExtractErrorInfo_APIKeyMasking(t *testing.T) {
	body := []byte(`{"error":{"message":"Bad key sk-1234567890abcdefghijklmnopqrstuv"}}`)
	info, err := ExtractErrorInfo(body, 401, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if strings.Contains(info.Message, "sk-123") {
		t.Errorf("message should have API key masked, got: %q", info.Message)
	}
	if !strings.Contains(info.Message, "[redacted]") {
		t.Errorf("message should contain [redacted], got: %q", info.Message)
	}
}

func TestExtractErrorInfo_SSEErrorStream(t *testing.T) {
	payload := []byte(`{"error":{"message":"Quota exceeded","type":"insufficient_quota"}}`)
	info, err := ExtractErrorInfo(payload, 429, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info from SSE payload")
	}
	if info.Class != "quota_exhausted" {
		t.Errorf("class = %q, want quota_exhausted", info.Class)
	}
}

func TestExtractErrorInfo_SSEMultiplePayloadsSkipsNonErrorEvents(t *testing.T) {
	payload := []byte("{\"type\":\"message_start\"}\n{\"error\":{\"message\":\"Quota exceeded\",\"type\":\"insufficient_quota\"}}")
	info, err := ExtractErrorInfo(payload, 429, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info from second SSE payload")
	}
	if info.Class != "quota_exhausted" {
		t.Errorf("class = %q, want quota_exhausted", info.Class)
	}
}

func TestExtractErrorInfo_JSONWithoutErrorShapeReturnsNil(t *testing.T) {
	info, err := ExtractErrorInfo([]byte(`{"type":"message_start"}`), 429, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Fatalf("info = %+v, want nil for non-error SSE payload", info)
	}
}

func TestExtractErrorInfo_ContextLengthError(t *testing.T) {
	body := []byte(`{"error":{"message":"This model's maximum context length is 128000 tokens.","type":"invalid_request_error","code":"context_length_exceeded"}}`)
	info, err := ExtractErrorInfo(body, 400, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Class != "context_length" {
		t.Errorf("class = %q, want context_length", info.Class)
	}
}

func TestExtractErrorInfo_Upstream5xx(t *testing.T) {
	body := []byte(`{"error":{"message":"Internal server error"}}`)
	info, err := ExtractErrorInfo(body, 500, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Class != "upstream_5xx" {
		t.Errorf("class = %q, want upstream_5xx", info.Class)
	}
}

func TestExtractErrorInfo_UnknownClass(t *testing.T) {
	body := []byte(`{"error":{"message":"Something strange happened"}}`)
	info, err := ExtractErrorInfo(body, 418, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Class != "unknown" {
		t.Errorf("class = %q, want unknown", info.Class)
	}
}

func TestExtractErrorInfo_Status403AuthFailed(t *testing.T) {
	body := []byte(`{"error":{"message":"Forbidden"}}`)
	info, err := ExtractErrorInfo(body, 403, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Class != "auth_failed" {
		t.Errorf("class = %q, want auth_failed", info.Class)
	}
}

func TestExtractErrorInfo_InvalidRequest400(t *testing.T) {
	body := []byte(`{"error":{"message":"Invalid request parameters"}}`)
	info, err := ExtractErrorInfo(body, 400, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if info.Class != "invalid_request" {
		t.Errorf("class = %q, want invalid_request", info.Class)
	}
}

func TestExtractErrorInfo_NewlineStripping(t *testing.T) {
	body := []byte("{\"error\":{\"message\":\"line1\\nline2\\r\\nline3\"}}")
	info, err := ExtractErrorInfo(body, 500, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if strings.Contains(info.Message, "\n") || strings.Contains(info.Message, "\r") {
		t.Errorf("message should not contain newlines, got: %q", info.Message)
	}
}

func TestExtractErrorInfo_NormalRequestDoesNotReadMessageContent(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"The answer is 42"}}]}`)
	info, err := ExtractErrorInfo(body, 200, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil && info.Message != "" {
		if info.Message == "The answer is 42" {
			t.Error("should not have extracted message content")
		}
	}
}

func TestExtractErrorInfo_BearerTokenMasking(t *testing.T) {
	body := []byte(`{"error":{"message":"Invalid token: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgN"}}`)
	info, err := ExtractErrorInfo(body, 401, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil error info")
	}
	if strings.Contains(info.Message, "eyJ") {
		t.Errorf("message should have token redacted, got: %q", info.Message)
	}
}
