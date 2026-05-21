package extractor

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

type ErrorInfo struct {
	Class            string
	Type             string
	Code             string
	Param            string
	Message          string
	MessageTruncated bool
}

func ExtractErrorInfo(sample []byte, status int, contentType string) (info *ErrorInfo, err error) {
	defer func() {
		if recover() != nil {
			info = nil
			err = nil
		}
	}()
	if len(sample) == 0 {
		return nil, nil
	}
	if strings.HasPrefix(strings.ToLower(contentType), "text/html") {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(sample))
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				return nil, nil
			}
			return nil, nil
		}
		info, err := extractErrorInfoFromJSON(raw, status)
		if err != nil {
			return nil, nil
		}
		if info != nil {
			return info, nil
		}
	}
}

func extractErrorInfoFromJSON(sample []byte, status int) (*ErrorInfo, error) {
	var errPayload struct {
		// OpenAI style: {"error": {"message": "...", "type": "...", "code": "...", "param": "..."}}
		Error *struct {
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Code    json.RawMessage `json:"code"`
			Param   string          `json:"param"`
			Status  string          `json:"status"`
		} `json:"error,omitempty"`
		// Anthropic style: {"type": "error", "error": {"type": "...", "message": "..."}}
		Type string `json:"type,omitempty"`
		// Generic style: {"message": "...", "code": "...", "type": "..."}
		Message string          `json:"message,omitempty"`
		Code    json.RawMessage `json:"code,omitempty"`
		Param   string          `json:"param,omitempty"`
		Status  string          `json:"status,omitempty"`
	}

	if err := json.Unmarshal(sample, &errPayload); err != nil {
		return nil, nil
	}

	info := &ErrorInfo{}
	switch {
	case errPayload.Error != nil:
		info.Type = errPayload.Error.Type
		if info.Type == "" {
			info.Type = errPayload.Error.Status
		}
		info.Code = rawJSONScalarString(errPayload.Error.Code)
		info.Param = errPayload.Error.Param
		info.Message = errPayload.Error.Message
	default:
		code := rawJSONScalarString(errPayload.Code)
		if errPayload.Message == "" && code == "" && errPayload.Status == "" {
			return nil, nil
		}
		info.Message = errPayload.Message
		info.Code = code
		if errPayload.Type != "" && errPayload.Type != "error" {
			info.Type = errPayload.Type
		}
		if info.Type == "" {
			info.Type = errPayload.Status
		}
		info.Param = errPayload.Param
	}
	if info.Type == "" && info.Code == "" && info.Param == "" && info.Message == "" {
		return nil, nil
	}

	info.Message, info.MessageTruncated = sanitizeMessage(info.Message)
	info.Class = classifyError(status, info.Type, info.Code, info.Message)
	return info, nil
}

func classifyError(status int, errType, errCode, message string) string {
	combined := strings.ToLower(errType + " " + errCode + " " + message)
	switch status {
	case 400:
		if isContextLengthError(combined) {
			return "context_length"
		}
		if containsAny(combined, "model_not_found", "model not found", "unknown model", "invalid model") {
			return "invalid_model"
		}
		return "invalid_request"
	case 401:
		if containsAny(combined, "invalid_api_key", "invalid api key", "incorrect api key") {
			return "auth_invalid_key"
		}
		if containsAny(combined, "expired", "revoked") {
			return "auth_expired"
		}
		return "auth_failed"
	case 402:
		return "billing_required"
	case 403:
		if containsAny(combined, "scope", "permission", "not allowed", "forbidden", "access denied") {
			return "permission_denied"
		}
		if containsAny(combined, "expired", "revoked") {
			return "auth_expired"
		}
		return "permission_denied"
	case 404:
		if containsAny(combined, "model") {
			return "invalid_model"
		}
		return "not_found"
	case 408:
		return "request_timeout"
	case 409:
		return "conflict"
	case 413:
		return "request_too_large"
	case 422:
		if isContextLengthError(combined) {
			return "context_length"
		}
		return "validation_error"
	}
	if status == 429 {
		if containsAny(combined, "quota", "insufficient", "balance", "credit", "resource_exhausted", "exhausted") {
			return "quota_exhausted"
		}
		if containsAny(combined, "rate", "limit", "too many") {
			return "rate_limited"
		}
		return "rate_limited"
	}
	if status >= 500 {
		return classifyUpstream5xx(status, combined)
	}
	return "unknown"
}

func classifyUpstream5xx(status int, combined string) string {
	if containsAny(combined, "timeout", "timed out", "deadline exceeded", "gateway timeout") {
		return "upstream_timeout"
	}
	if containsAny(combined, "connection refused", "connect refused", "refused") {
		return "upstream_connection_refused"
	}
	if containsAny(combined, "connection reset", "reset by peer") {
		return "upstream_connection_reset"
	}
	if containsAny(combined, "dns", "no such host", "name resolution") {
		return "upstream_dns_error"
	}
	if containsAny(combined, "no route", "network unreachable") {
		return "upstream_network_unreachable"
	}
	if containsAny(combined, "tls", "certificate", "x509") {
		return "upstream_tls_error"
	}
	if containsAny(combined, "overloaded", "overload", "capacity") {
		return "upstream_overloaded"
	}
	if containsAny(combined, "temporarily unavailable", "service unavailable", "unavailable") {
		return "upstream_unavailable"
	}
	switch status {
	case 500:
		return "upstream_internal_error"
	case 501:
		return "upstream_not_implemented"
	case 502:
		return "upstream_bad_gateway"
	case 503:
		return "upstream_unavailable"
	case 504:
		return "upstream_timeout"
	default:
		return "upstream_5xx"
	}
}

func rawJSONScalarString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var n json.Number
	if err := dec.Decode(&n); err == nil {
		return n.String()
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		if b {
			return "true"
		}
		return "false"
	}
	return ""
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func isContextLengthError(s string) bool {
	return containsAny(s,
		"context_length",
		"context length",
		"context window",
		"maximum context",
		"max context",
		"token limit",
		"too many tokens",
		"maximum number of tokens",
		"exceeded token",
	)
}

var (
	// Matches suspected API keys: sk-..., JWTs, and long hex strings.
	keyLikePattern        = regexp.MustCompile(`\b(sk-[a-zA-Z0-9_-]{20,}|[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|[0-9a-fA-F]*[0-9][0-9a-fA-F]{31,})\b`)
	base64LikePattern     = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{40,}={0,2}\b`)
	base64SignalCharClass = regexp.MustCompile(`[0-9+/_=-]`)
)

func sanitizeMessage(msg string) (string, bool) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "", false
	}

	// Replace newlines and control characters with spaces
	msg = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' || r == 127 {
			return ' '
		}
		return r
	}, msg)

	// Mask suspected API keys / tokens.
	msg = keyLikePattern.ReplaceAllString(msg, "[redacted]")
	msg = base64LikePattern.ReplaceAllStringFunc(msg, func(candidate string) string {
		if base64SignalCharClass.MatchString(candidate) {
			return "[redacted]"
		}
		return candidate
	})

	// Truncate to 500 UTF-8 bytes
	const maxBytes = 500
	if len(msg) <= maxBytes {
		return msg, false
	}
	truncated := true
	for i := maxBytes; i >= maxBytes-4 && i >= 0; i-- {
		if utf8.RuneStart(msg[i]) {
			return msg[:i], truncated
		}
	}
	return msg[:maxBytes], truncated
}
