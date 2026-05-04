package extractor

import (
	"bytes"
	"encoding/json"
	"strings"
)

type UsageInfo struct {
	Model               string `json:"model"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	ReasoningTokens     int64  `json:"reasoning_tokens"`
	CachedTokens        int64  `json:"cached_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	TotalTokens         int64  `json:"total_tokens"`
	UsageRawJSON        string `json:"usage_raw_json,omitempty"`
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
	if !hasChatUsageTokens(&usage) {
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

// ExtractAnthropicUsage parses an Anthropic Messages SSE "data:" line.
func ExtractAnthropicUsage(data []byte) (*UsageInfo, error) {
	text := stripSSEPrefix(string(data))
	if text == "" {
		return nil, nil
	}

	var event struct {
		Type    string            `json:"type"`
		Message *anthropicMessage `json:"message"`
		Usage   json.RawMessage   `json:"usage"`
		Model   string            `json:"model"`
	}
	if err := json.Unmarshal([]byte(text), &event); err != nil {
		return nil, err
	}
	if event.Message != nil && len(event.Message.Usage) > 0 {
		return anthropicUsageInfo(event.Message.Model, event.Message.Usage)
	}
	if len(event.Usage) > 0 {
		return anthropicUsageInfo(event.Model, event.Usage)
	}
	return nil, nil
}

// ExtractGeminiUsage parses a Gemini streamGenerateContent SSE "data:" line.
func ExtractGeminiUsage(data []byte) (*UsageInfo, error) {
	text := stripSSEPrefix(string(data))
	if text == "" {
		return nil, nil
	}
	return geminiJSONToInfo([]byte(text))
}

// ---------- non-streaming extraction ----------

// ExtractNonStreaming parses a complete JSON response body for usage.
// endpoint is used to prefer the correct parser for the API.
// Returns (nil, nil) when there is no usage field in valid JSON.
// Returns (nil, err) on JSON parse failure.
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
		if hasChatUsageTokens(&usage) {
			info := chatUsageToInfo(generic.Model, &usage)
			info.UsageRawJSON = rawUsageString(generic.Usage)
			return info, nil
		}
	}

	return nil, nil
}

// ExtractAnthropicNonStreaming parses an Anthropic Messages JSON response body.
func ExtractAnthropicNonStreaming(body []byte) (*UsageInfo, error) {
	body = bytes.TrimSpace(body)
	var resp anthropicMessage
	if err := decodeJSON(body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Usage) == 0 {
		return nil, nil
	}
	return anthropicUsageInfo(resp.Model, resp.Usage)
}

// ExtractGeminiNonStreaming parses Gemini generateContent JSON response bodies.
// It also accepts a JSON array of streamed chunks for clients that do not use SSE.
func ExtractGeminiNonStreaming(body []byte) (*UsageInfo, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, nil
	}
	if body[0] == '[' {
		var chunks []geminiResponse
		if err := decodeJSON(body, &chunks); err != nil {
			return nil, err
		}
		for i := len(chunks) - 1; i >= 0; i-- {
			info, err := geminiResponseToInfo(&chunks[i])
			if err != nil {
				return nil, err
			}
			if info != nil {
				return info, nil
			}
		}
		return nil, nil
	}
	return geminiJSONToInfo(body)
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
	if !hasChatUsageTokens(&usage) {
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
		InputTokens:  tokenCount(u.PromptTokens),
		OutputTokens: tokenCount(u.CompletionTokens),
		TotalTokens:  tokenCount(u.TotalTokens),
	}
	if u.PromptTokensDetails != nil {
		info.CachedTokens = tokenCount(u.PromptTokensDetails.CachedTokens)
	}
	if u.CompletionTokensDetails != nil {
		info.ReasoningTokens = tokenCount(u.CompletionTokensDetails.ReasoningTokens)
	}
	return info
}

func responsesUsageToInfo(model string, u *responsesUsage) *UsageInfo {
	info := &UsageInfo{
		Model:        model,
		InputTokens:  tokenCount(u.InputTokens),
		OutputTokens: tokenCount(u.OutputTokens),
		TotalTokens:  tokenCount(u.TotalTokens),
	}
	if u.InputTokensDetails != nil {
		info.CachedTokens = tokenCount(u.InputTokensDetails.CachedTokens)
	}
	if u.OutputTokensDetails != nil {
		info.ReasoningTokens = tokenCount(u.OutputTokensDetails.ReasoningTokens)
	}
	return info
}

func anthropicUsageInfo(model string, raw json.RawMessage) (*UsageInfo, error) {
	var usage anthropicUsage
	if err := json.Unmarshal(raw, &usage); err != nil {
		return nil, err
	}
	if !hasAnthropicUsageTokens(&usage) {
		return nil, nil
	}
	info := anthropicUsageToInfo(model, &usage)
	info.UsageRawJSON = rawUsageString(raw)
	return info, nil
}

func anthropicUsageToInfo(model string, u *anthropicUsage) *UsageInfo {
	inputTokens := sumTokenCounts(u.InputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens)
	outputTokens := tokenCount(u.OutputTokens)
	totalTokens := sumTokenCounts(inputTokens, outputTokens)
	return &UsageInfo{
		Model:               model,
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
		CachedTokens:        tokenCount(u.CacheReadInputTokens),
		CacheCreationTokens: tokenCount(u.CacheCreationInputTokens),
		TotalTokens:         totalTokens,
	}
}

func hasAnthropicUsageTokens(u *anthropicUsage) bool {
	return u.InputTokens > 0 ||
		u.OutputTokens > 0 ||
		u.CacheCreationInputTokens > 0 ||
		u.CacheReadInputTokens > 0
}

func geminiJSONToInfo(body []byte) (*UsageInfo, error) {
	var resp geminiResponse
	if err := decodeJSON(body, &resp); err != nil {
		return nil, err
	}
	return geminiResponseToInfo(&resp)
}

func geminiResponseToInfo(resp *geminiResponse) (*UsageInfo, error) {
	if resp == nil || len(resp.UsageMetadata) == 0 {
		return nil, nil
	}
	var usage geminiUsage
	if err := json.Unmarshal(resp.UsageMetadata, &usage); err != nil {
		return nil, err
	}
	if !hasGeminiUsageTokens(&usage) {
		return nil, nil
	}
	model := resp.ModelVersion
	if model == "" {
		model = resp.Model
	}
	info := geminiUsageToInfo(model, &usage)
	info.UsageRawJSON = rawUsageString(resp.UsageMetadata)
	return info, nil
}

func geminiUsageToInfo(model string, u *geminiUsage) *UsageInfo {
	promptTokens := tokenCount(u.PromptTokenCount)
	toolUseTokens := tokenCount(u.ToolUsePromptTokenCount)
	cachedTokens := tokenCount(u.CachedContentTokenCount)
	thoughtTokens := tokenCount(u.ThoughtsTokenCount)

	inputTokens := sumTokenCounts(promptTokens, toolUseTokens)
	if inputTokens == 0 && cachedTokens > 0 {
		inputTokens = cachedTokens
	}
	outputTokens := sumTokenCounts(u.CandidatesTokenCount, thoughtTokens)
	providerTotal := tokenCount(u.TotalTokenCount)
	if outputTokens == 0 && providerTotal > inputTokens {
		outputTokens = providerTotal - inputTokens
	}
	totalTokens := providerTotal
	if totalTokens == 0 {
		totalTokens = sumTokenCounts(inputTokens, outputTokens)
	}
	return &UsageInfo{
		Model:           model,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		ReasoningTokens: thoughtTokens,
		CachedTokens:    cachedTokens,
		TotalTokens:     totalTokens,
	}
}

func hasGeminiUsageTokens(u *geminiUsage) bool {
	return u.PromptTokenCount > 0 ||
		u.CandidatesTokenCount > 0 ||
		u.TotalTokenCount > 0 ||
		u.CachedContentTokenCount > 0 ||
		u.ThoughtsTokenCount > 0 ||
		u.ToolUsePromptTokenCount > 0
}

func hasChatUsageTokens(u *chatUsage) bool {
	return u.PromptTokens > 0 ||
		u.CompletionTokens > 0 ||
		u.TotalTokens > 0
}

func tokenCount(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

const maxTokenCount = int64(9223372036854775807)

func sumTokenCounts(values ...int64) int64 {
	var total int64
	for _, value := range values {
		value = tokenCount(value)
		if value > maxTokenCount-total {
			return maxTokenCount
		}
		total += value
	}
	return total
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

type anthropicMessage struct {
	Model string          `json:"model"`
	Usage json.RawMessage `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type geminiResponse struct {
	Model         string          `json:"model"`
	ModelVersion  string          `json:"modelVersion"`
	UsageMetadata json.RawMessage `json:"usageMetadata"`
}

type geminiUsage struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
	ToolUsePromptTokenCount int64 `json:"toolUsePromptTokenCount"`
}
