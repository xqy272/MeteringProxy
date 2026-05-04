package extractor

import (
	"strings"
	"testing"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		errType string
		errCode string
		message string
		want    string
	}{
		{"auth 401", 401, "", "", "", "auth_failed"},
		{"auth 403", 403, "", "", "", "auth_failed"},
		{"quota 429", 429, "", "", "insufficient_quota", "quota_exhausted"},
		{"rate 429", 429, "rate_limit", "", "", "rate_limited"},
		{"rate 429 default", 429, "", "", "", "rate_limited"},
		{"context_length 400", 400, "", "", "context_length_exceeded", "context_length"},
		{"invalid_request 400", 400, "", "invalid_api_key", "", "invalid_request"},
		{"upstream 500", 500, "", "", "", "upstream_5xx"},
		{"upstream 502", 502, "", "", "", "upstream_5xx"},
		{"unknown 418", 418, "", "", "", "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyError(tc.status, tc.errType, tc.errCode, tc.message)
			if got != tc.want {
				t.Errorf("classifyError(%d, %q, %q, %q) = %q, want %q", tc.status, tc.errType, tc.errCode, tc.message, got, tc.want)
			}
		})
	}
}

func TestExtractErrorInfo_OpenAIStyle(t *testing.T) {
	body := []byte(`{"error":{"message":"Rate limit reached","type":"rate_limit_error","code":"rate_limit_exceeded","param":null}}`)
	info, err := ExtractErrorInfo(body, 429, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil ErrorInfo")
	}
	if info.Class != "rate_limited" {
		t.Errorf("class = %q, want rate_limited", info.Class)
	}
	if info.Type != "rate_limit_error" {
		t.Errorf("type = %q, want rate_limit_error", info.Type)
	}
	if info.Code != "rate_limit_exceeded" {
		t.Errorf("code = %q, want rate_limit_exceeded", info.Code)
	}
	if info.Message != "Rate limit reached" {
		t.Errorf("message = %q, want Rate limit reached", info.Message)
	}
	if info.MessageTruncated {
		t.Error("message should not be truncated")
	}
}

func TestExtractErrorInfo_AnthropicStyle(t *testing.T) {
	body := []byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	info, err := ExtractErrorInfo(body, 401, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil ErrorInfo")
	}
	if info.Class != "auth_failed" {
		t.Errorf("class = %q, want auth_failed", info.Class)
	}
}

func TestExtractErrorInfo_GoogleStyleNumericCode(t *testing.T) {
	body := []byte(`{"error":{"code":400,"message":"Invalid JSON payload received","status":"INVALID_ARGUMENT"}}`)
	info, err := ExtractErrorInfo(body, 400, "application/json; charset=utf-8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil ErrorInfo")
	}
	if info.Class != "invalid_request" {
		t.Errorf("class = %q, want invalid_request", info.Class)
	}
	if info.Type != "INVALID_ARGUMENT" {
		t.Errorf("type = %q, want INVALID_ARGUMENT", info.Type)
	}
	if info.Code != "400" {
		t.Errorf("code = %q, want 400", info.Code)
	}
}

func TestExtractErrorInfo_ContextLength(t *testing.T) {
	body := []byte(`{"error":{"message":"context_length_exceeded: maximum context length is 128k","type":"invalid_request_error","code":"context_length_exceeded"}}`)
	info, err := ExtractErrorInfo(body, 400, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil ErrorInfo")
	}
	if info.Class != "context_length" {
		t.Errorf("class = %q, want context_length", info.Class)
	}
}

func TestExtractErrorInfo_Upstream5xx(t *testing.T) {
	body := []byte(`{"error":{"message":"Internal server error","type":"server_error"}}`)
	info, err := ExtractErrorInfo(body, 500, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil ErrorInfo")
	}
	if info.Class != "upstream_5xx" {
		t.Errorf("class = %q, want upstream_5xx", info.Class)
	}
}

func TestExtractErrorInfo_HTMLContentType_Skipped(t *testing.T) {
	body := []byte(`<html><body>Error</body></html>`)
	info, err := ExtractErrorInfo(body, 500, "text/html; charset=utf-8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for HTML content type")
	}
}

func TestExtractErrorInfo_EmptyBody(t *testing.T) {
	info, err := ExtractErrorInfo(nil, 500, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for empty body")
	}
}

func TestExtractErrorInfo_NilOnNoError(t *testing.T) {
	body := []byte(`{"id":"chatcmpl-123","model":"gpt-4o"}`)
	info, err := ExtractErrorInfo(body, 200, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Error("expected nil for successful response")
	}
}

func TestSanitizeMessage_KeyRedaction(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantSub string
		wantNot string
	}{
		{"sk- key", "Error: invalid key sk-proj-abc1234567890abcdef1234567890", "[redacted]", "sk-proj-abc"},
		{"JWT-like", "Authorization: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozj4Qh7vH8Lz9OQv3eX", "[redacted]", "eyJhbGci"},
		{"long hex", "token: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", "[redacted]", "a1b2c3d4"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := sanitizeMessage(tc.input)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("sanitizeMessage(%q) = %q, want to contain %q", tc.input, got, tc.wantSub)
			}
			if strings.Contains(got, tc.wantNot) {
				t.Errorf("sanitizeMessage(%q) = %q, should NOT contain %q", tc.input, got, tc.wantNot)
			}
		})
	}
}

func TestSanitizeMessage_Truncation(t *testing.T) {
	longMsg := strings.Repeat("a", 600)
	got, truncated := sanitizeMessage(longMsg)
	if !truncated {
		t.Error("expected truncated = true for long message")
	}
	if len(got) > 500 {
		t.Errorf("sanitized message length = %d, want <= 500", len(got))
	}
	if !isValidUTF8(got) {
		t.Error("sanitized message is not valid UTF-8")
	}
}

func TestSanitizeMessage_UTF8Boundary(t *testing.T) {
	msg := strings.Repeat("α", 300)
	got, truncated := sanitizeMessage(msg)
	if !truncated {
		t.Error("expected truncated = true")
	}
	if !isValidUTF8(got) {
		t.Errorf("message is not valid UTF-8: %q", got)
	}
}

func TestSanitizeMessage_EmptyString(t *testing.T) {
	got, truncated := sanitizeMessage("")
	if got != "" {
		t.Errorf("empty string should return empty, got %q", got)
	}
	if truncated {
		t.Error("empty string should not be truncated")
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}
