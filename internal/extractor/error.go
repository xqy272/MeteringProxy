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
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
			Param   string `json:"param"`
		} `json:"error,omitempty"`
		// Anthropic style: {"type": "error", "error": {"type": "...", "message": "..."}}
		Type string `json:"type,omitempty"`
		// Generic style: {"message": "...", "code": "...", "type": "..."}
		Message string `json:"message,omitempty"`
		Code    string `json:"code,omitempty"`
		Param   string `json:"param,omitempty"`
	}

	if err := json.Unmarshal(sample, &errPayload); err != nil {
		return nil, nil
	}

	info := &ErrorInfo{}
	switch {
	case errPayload.Error != nil:
		info.Type = errPayload.Error.Type
		info.Code = errPayload.Error.Code
		info.Param = errPayload.Error.Param
		info.Message = errPayload.Error.Message
	default:
		if errPayload.Message == "" && errPayload.Code == "" {
			return nil, nil
		}
		info.Message = errPayload.Message
		info.Code = errPayload.Code
		if errPayload.Type != "" && errPayload.Type != "error" {
			info.Type = errPayload.Type
		}
	}
	if info.Type == "" && info.Code == "" && info.Param == "" && info.Message == "" {
		return nil, nil
	}

	info.Message, info.MessageTruncated = sanitizeMessage(info.Message)
	info.Class = classifyError(status, info.Type, info.Code, info.Message)
	return info, nil
}

func classifyError(status int, errType, errCode, message string) string {
	if status == 401 || status == 403 {
		return "auth_failed"
	}
	if status == 429 {
		combined := strings.ToLower(errType + " " + errCode + " " + message)
		if containsAny(combined, "quota", "insufficient", "balance", "credit") {
			return "quota_exhausted"
		}
		if containsAny(combined, "rate", "limit", "too many") {
			return "rate_limited"
		}
		return "rate_limited"
	}
	if status == 400 {
		combined := strings.ToLower(errType + " " + errCode + " " + message)
		if containsAny(combined, "context", "token limit", "maximum context") {
			return "context_length"
		}
		return "invalid_request"
	}
	if status >= 500 {
		return "upstream_5xx"
	}
	return "unknown"
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
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
