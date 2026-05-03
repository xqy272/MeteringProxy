package extractor

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type UsageInfo struct {
	Model           string `json:"model"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	UsageRawJSON    string `json:"usage_raw_json,omitempty"`
}

// ---------- SSE extraction (used by streaming path) ----------

// ExtractChatUsage parses an SSE "data:" line from a chat completions stream.
func ExtractChatUsage(data []byte) (*UsageInfo, error) {
	text := stripSSEPrefix(string(data))
	if text == "" {
		return nil, nil
	}

	var resp struct {
		Model string          `json:"model"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return nil, err
	}
	if len(resp.Usage) == 0 {
		return nil, nil
	}
	var usage chatUsage
	if err := json.Unmarshal(resp.Usage, &usage); err != nil {
		return nil, err
	}
	// Reject responses-format usage: has input_tokens/output_tokens but no chat-format fields.
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.InputTokens > 0 {
		return nil, nil
	}
	info := chatUsageToInfo(resp.Model, &usage)
	info.UsageRawJSON = rawUsageString(resp.Usage)
	return info, nil
}

// ExtractResponsesUsage parses an SSE "data:" line from a responses stream.
func ExtractResponsesUsage(data []byte) (*UsageInfo, error) {
	text := stripSSEPrefix(string(data))
	if text == "" {
		return nil, nil
	}

	var event struct {
		Type     string             `json:"type"`
		Response *responsesResponse `json:"response"`
	}
	if err := json.Unmarshal([]byte(text), &event); err != nil {
		return nil, err
	}
	if event.Type != "response.completed" || event.Response == nil || len(event.Response.Usage) == 0 {
		return nil, nil
	}
	var usage responsesUsage
	if err := json.Unmarshal(event.Response.Usage, &usage); err != nil {
		return nil, err
	}
	info := responsesUsageToInfo(event.Response.Model, &usage)
	info.UsageRawJSON = rawUsageString(event.Response.Usage)
	return info, nil
}

// ---------- non-streaming extraction ----------

var errNoUsage = io.EOF // sentinel: valid JSON but no usage field

// ExtractNonStreaming parses a complete JSON response body for usage.
// endpoint is used to prefer the correct parser for the API.
// Returns (nil, nil) when there is no usage field in valid JSON.
// Returns (nil, err) on JSON parse failure (err is never io.EOF in that case).
func ExtractNonStreaming(body []byte, endpoint string) (*UsageInfo, error) {
	body = bytes.TrimSpace(body)

	if strings.Contains(endpoint, "responses") {
		if u, err := tryResponsesFormat(body); u != nil || err != nil {
			return u, err
		}
		if u, err := tryChatFormat(body); u != nil || err != nil {
			return u, err
		}
	} else {
		if u, err := tryChatFormat(body); u != nil || err != nil {
			return u, err
		}
		if u, err := tryResponsesFormat(body); u != nil || err != nil {
			return u, err
		}
	}

	// Generic fallback
	var generic struct {
		Model string          `json:"model"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := decodeJSON(body, &generic); err != nil {
		// Check if it's a real parse error vs trailing-garbage after good JSON.
		// decodeJSON uses json.Decoder which ignores trailing data, so a real
		// syntax error indicates the JSON itself is broken.
		return nil, err
	}
	if len(generic.Usage) > 0 {
		var usage chatUsage
		if err := json.Unmarshal(generic.Usage, &usage); err != nil {
			return nil, err
		}
		if usage.PromptTokens > 0 || usage.TotalTokens > 0 {
			info := chatUsageToInfo(generic.Model, &usage)
			info.UsageRawJSON = rawUsageString(generic.Usage)
			return info, nil
		}
	}

	return nil, nil
}

func tryChatFormat(body []byte) (*UsageInfo, error) {
	var resp struct {
		Model string          `json:"model"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := decodeJSON(body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Usage) == 0 {
		return nil, nil
	}
	var usage chatUsage
	if err := json.Unmarshal(resp.Usage, &usage); err != nil {
		return nil, err
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.InputTokens > 0 {
		return nil, nil
	}
	info := chatUsageToInfo(resp.Model, &usage)
	info.UsageRawJSON = rawUsageString(resp.Usage)
	return info, nil
}

func tryResponsesFormat(body []byte) (*UsageInfo, error) {
	var resp struct {
		Model string          `json:"model"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := decodeJSON(body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Usage) == 0 {
		return nil, nil
	}
	var usage responsesUsage
	if err := json.Unmarshal(resp.Usage, &usage); err != nil {
		return nil, err
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return nil, nil
	}
	info := responsesUsageToInfo(resp.Model, &usage)
	info.UsageRawJSON = rawUsageString(resp.Usage)
	return info, nil
}

// decodeJSON decodes the first JSON value from data, tolerating trailing non-JSON bytes.
func decodeJSON(data []byte, v any) error {
	return json.NewDecoder(bytes.NewReader(data)).Decode(v)
}

// ---------- helpers ----------

func stripSSEPrefix(text string) string {
	text = strings.TrimSpace(text)
	for strings.HasPrefix(text, "data:") {
		text = strings.TrimSpace(text[5:])
	}
	if text == "[DONE]" || text == "" {
		return ""
	}
	return text
}

func rawUsageString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err == nil {
		return compact.String()
	}
	return string(raw)
}

func chatUsageToInfo(model string, u *chatUsage) *UsageInfo {
	info := &UsageInfo{
		Model:        model,
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		info.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil {
		info.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return info
}

func responsesUsageToInfo(model string, u *responsesUsage) *UsageInfo {
	info := &UsageInfo{
		Model:        model,
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.InputTokensDetails != nil {
		info.CachedTokens = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		info.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	return info
}

// ---------- JSON types ----------

type chatUsage struct {
	PromptTokens            int64                    `json:"prompt_tokens"`
	CompletionTokens        int64                    `json:"completion_tokens"`
	TotalTokens             int64                    `json:"total_tokens"`
	InputTokens             int64                    `json:"input_tokens"`
	PromptTokensDetails     *promptTokensDetails     `json:"prompt_tokens_details"`
	CompletionTokensDetails *completionTokensDetails `json:"completion_tokens_details"`
}

type promptTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

type completionTokensDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

type responsesResponse struct {
	Model string          `json:"model"`
	Usage json.RawMessage `json:"usage"`
}

type responsesUsage struct {
	InputTokens         int64                `json:"input_tokens"`
	OutputTokens        int64                `json:"output_tokens"`
	TotalTokens         int64                `json:"total_tokens"`
	InputTokensDetails  *inputTokensDetails  `json:"input_tokens_details"`
	OutputTokensDetails *outputTokensDetails `json:"output_tokens_details"`
}

type inputTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

type outputTokensDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}
