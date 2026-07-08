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

	InputTextTokens   int64  `json:"input_text_tokens"`
	InputImageTokens  int64  `json:"input_image_tokens"`
	CachedTextTokens  int64  `json:"cached_text_tokens"`
	CachedImageTokens int64  `json:"cached_image_tokens"`
	ImageCount        int64  `json:"image_count"`
	PartialImageCount int64  `json:"partial_image_count"`
	InputImageCount   int64  `json:"input_image_count"`
	Operation         string `json:"operation,omitempty"`
	Size              string `json:"size,omitempty"`
	Quality           string `json:"quality,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	HasMask           bool   `json:"has_mask,omitempty"`
}

// ---------- SSE extraction (used by streaming path) ----------

// ExtractChatUsage parses an SSE "data:" line from a chat completions stream.
func ExtractChatUsage(data []byte) (*UsageInfo, error) {
	data = stripSSEPrefixBytes(data)
	if len(data) == 0 {
		return nil, nil
	}
	if !bytes.Contains(data, []byte("usage")) && !bytes.Contains(data, []byte(`\u`)) && jsonObjectValid(data) {
		return nil, nil
	}

	var resp struct {
		Model string          `json:"model"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
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
	if strings.Contains(event.Type, "image_generation_call.partial_image") {
		return &UsageInfo{PartialImageCount: 1, Operation: "generation"}, nil
	}
	if event.Type != "response.completed" || event.Response == nil {
		return nil, nil
	}
	imageCount := countResponsesImageOutputs(event.Response.Output)
	if len(event.Response.Usage) == 0 {
		if imageCount > 0 {
			return &UsageInfo{Model: event.Response.Model, ImageCount: imageCount, Operation: "generation"}, nil
		}
		return nil, nil
	}
	var usage responsesUsage
	if err := json.Unmarshal(event.Response.Usage, &usage); err != nil {
		return nil, err
	}
	info := responsesUsageToInfo(event.Response.Model, &usage)
	if imageCount > 0 {
		info.ImageCount = imageCount
		info.Operation = "generation"
	}
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

func ExtractImageUsage(data []byte) (*UsageInfo, error) {
	text := stripSSEPrefix(string(data))
	if text == "" {
		return nil, nil
	}
	var event struct {
		Type              string          `json:"type"`
		Usage             json.RawMessage `json:"usage"`
		Model             string          `json:"model"`
		PartialImageIndex *int            `json:"partial_image_index"`
	}
	if err := json.Unmarshal([]byte(text), &event); err != nil {
		return nil, err
	}
	if strings.Contains(event.Type, "partial_image") {
		return &UsageInfo{PartialImageCount: 1}, nil
	}
	if !strings.Contains(event.Type, "completed") && len(event.Usage) == 0 {
		return nil, nil
	}
	return imageJSONToInfo([]byte(text))
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
			if err == nil {
				return u, nil
			}
			if sampleInfo, ok := responsesSampleToInfo(body); ok {
				return sampleInfo, nil
			}
			return nil, err
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
			if sampleInfo, ok := geminiSampleToInfo(body); ok {
				return sampleInfo, nil
			}
			return nil, err
		}
		var imageCount int64
		for i := range chunks {
			imageCount += countGeminiImageOutputs(&chunks[i])
		}
		for i := len(chunks) - 1; i >= 0; i-- {
			info, err := geminiResponseToInfo(&chunks[i])
			if err != nil {
				return nil, err
			}
			if info != nil {
				if imageCount > 0 {
					info.ImageCount = imageCount
					info.Operation = "generation"
				}
				return info, nil
			}
		}
		if imageCount > 0 {
			model := ""
			for i := len(chunks) - 1; i >= 0; i-- {
				model = geminiResponseModel(&chunks[i])
				if model != "" {
					break
				}
			}
			return &UsageInfo{Model: model, ImageCount: imageCount, Operation: "generation"}, nil
		}
		return nil, nil
	}
	info, err := geminiJSONToInfo(body)
	if err == nil {
		return info, nil
	}
	if sampleInfo, ok := geminiSampleToInfo(body); ok {
		return sampleInfo, nil
	}
	return nil, err
}

func ExtractImageNonStreaming(body []byte) (*UsageInfo, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, nil
	}
	info, err := imageJSONToInfo(body)
	if err == nil {
		return info, nil
	}
	if sampleInfo, ok := imageSampleToInfo(body); ok {
		return sampleInfo, nil
	}
	return nil, err
}

func ExtractEmbeddingNonStreaming(body []byte) (*UsageInfo, error) {
	body = bytes.TrimSpace(body)
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
	if usage.PromptTokens == 0 && usage.TotalTokens == 0 {
		return nil, nil
	}
	info := &UsageInfo{
		Model:        resp.Model,
		InputTokens:  tokenCount(usage.PromptTokens),
		TotalTokens:  tokenCount(usage.TotalTokens),
		UsageRawJSON: rawUsageString(resp.Usage),
	}
	return info, nil
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
		Model  string                `json:"model"`
		Usage  json.RawMessage       `json:"usage"`
		Output []responsesOutputItem `json:"output"`
	}
	if err := decodeJSON(body, &resp); err != nil {
		return nil, err
	}
	imageCount := countResponsesImageOutputs(resp.Output)
	if len(resp.Usage) == 0 {
		if imageCount > 0 {
			return &UsageInfo{Model: resp.Model, ImageCount: imageCount, Operation: "generation"}, nil
		}
		return nil, nil
	}
	var usage responsesUsage
	if err := json.Unmarshal(resp.Usage, &usage); err != nil {
		return nil, err
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && imageCount == 0 {
		return nil, nil
	}
	info := responsesUsageToInfo(resp.Model, &usage)
	if imageCount > 0 {
		info.ImageCount = imageCount
		info.Operation = "generation"
	}
	info.UsageRawJSON = rawUsageString(resp.Usage)
	return info, nil
}

func responsesSampleToInfo(body []byte) (*UsageInfo, bool) {
	info := &UsageInfo{
		Model:      findJSONStringField(body, "model"),
		ImageCount: int64(bytes.Count(body, []byte(`"image_generation_call"`))),
	}
	if info.ImageCount > 0 {
		info.Operation = "generation"
	}
	if raw, ok := findJSONFieldRaw(body, "usage"); ok {
		var usage responsesUsage
		if err := json.Unmarshal(raw, &usage); err == nil {
			usageInfo := responsesUsageToInfo(info.Model, &usage)
			usageInfo.ImageCount = info.ImageCount
			usageInfo.Operation = info.Operation
			usageInfo.UsageRawJSON = rawUsageString(raw)
			info = usageInfo
		}
	}
	if info.ImageCount == 0 && info.InputTokens == 0 && info.OutputTokens == 0 && info.TotalTokens == 0 {
		return nil, false
	}
	return info, true
}

func imageJSONToInfo(body []byte) (*UsageInfo, error) {
	var resp struct {
		Model string            `json:"model"`
		Type  string            `json:"type"`
		Data  []json.RawMessage `json:"data"`
		Usage json.RawMessage   `json:"usage"`
	}
	if err := decodeJSON(body, &resp); err != nil {
		return nil, err
	}
	info := &UsageInfo{
		Model:      resp.Model,
		ImageCount: int64(len(resp.Data)),
	}
	if len(resp.Usage) > 0 {
		var usage imageUsage
		if err := json.Unmarshal(resp.Usage, &usage); err != nil {
			return nil, err
		}
		applyImageUsage(info, &usage)
		info.UsageRawJSON = rawUsageString(resp.Usage)
	}
	if info.ImageCount == 0 && (info.InputTokens != 0 || info.OutputTokens != 0 || info.TotalTokens != 0) {
		info.ImageCount = 1
	}
	if info.ImageCount == 0 && info.InputTokens == 0 && info.OutputTokens == 0 && info.TotalTokens == 0 {
		return nil, nil
	}
	return info, nil
}

func imageSampleToInfo(body []byte) (*UsageInfo, bool) {
	info := &UsageInfo{
		Model:      findJSONStringField(body, "model"),
		ImageCount: countImageOutputMarkers(body),
	}
	if raw, ok := findJSONFieldRaw(body, "usage"); ok {
		var usage imageUsage
		if err := json.Unmarshal(raw, &usage); err == nil {
			applyImageUsage(info, &usage)
			info.UsageRawJSON = rawUsageString(raw)
		}
	}
	if info.ImageCount == 0 && (info.InputTokens != 0 || info.OutputTokens != 0 || info.TotalTokens != 0) {
		info.ImageCount = 1
	}
	if info.ImageCount == 0 && info.InputTokens == 0 && info.OutputTokens == 0 && info.TotalTokens == 0 {
		return nil, false
	}
	return info, true
}

func applyImageUsage(info *UsageInfo, usage *imageUsage) {
	if info == nil || usage == nil {
		return
	}
	info.InputTokens = tokenCount(usage.InputTokens)
	info.OutputTokens = tokenCount(usage.OutputTokens)
	info.TotalTokens = tokenCount(usage.TotalTokens)
	if usage.InputTokensDetails != nil {
		info.InputTextTokens = tokenCount(usage.InputTokensDetails.TextTokens)
		info.InputImageTokens = tokenCount(usage.InputTokensDetails.ImageTokens)
		info.CachedTokens = tokenCount(usage.InputTokensDetails.CachedTokens)
		info.CachedTextTokens = tokenCount(usage.InputTokensDetails.CachedTextTokens)
		info.CachedImageTokens = tokenCount(usage.InputTokensDetails.CachedImageTokens)
	}
}

func countImageOutputMarkers(body []byte) int64 {
	return int64(bytes.Count(body, []byte(`"b64_json"`)) + bytes.Count(body, []byte(`"url"`)))
}

func findJSONStringField(body []byte, field string) string {
	raw, ok := findJSONFieldRaw(body, field)
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func countJSONStringFieldPrefix(body []byte, field, prefix string) int64 {
	pattern := []byte(`"` + field + `"`)
	var count int64
	for offset := 0; offset < len(body); {
		idx := bytes.Index(body[offset:], pattern)
		if idx < 0 {
			return count
		}
		pos := offset + idx + len(pattern)
		pos = skipJSONSpace(body, pos)
		if pos >= len(body) || body[pos] != ':' {
			offset += idx + len(pattern)
			continue
		}
		pos = skipJSONSpace(body, pos+1)
		var raw json.RawMessage
		if err := json.NewDecoder(bytes.NewReader(body[pos:])).Decode(&raw); err == nil && len(raw) > 0 {
			var value string
			if err := json.Unmarshal(raw, &value); err == nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), prefix) {
				count++
			}
		}
		offset += idx + len(pattern)
	}
	return count
}

func findJSONFieldRaw(body []byte, field string) (json.RawMessage, bool) {
	pattern := []byte(`"` + field + `"`)
	for offset := 0; offset < len(body); {
		idx := bytes.Index(body[offset:], pattern)
		if idx < 0 {
			return nil, false
		}
		pos := offset + idx + len(pattern)
		pos = skipJSONSpace(body, pos)
		if pos >= len(body) || body[pos] != ':' {
			offset += idx + len(pattern)
			continue
		}
		pos = skipJSONSpace(body, pos+1)
		if pos >= len(body) {
			return nil, false
		}
		var raw json.RawMessage
		if err := json.NewDecoder(bytes.NewReader(body[pos:])).Decode(&raw); err == nil && len(raw) > 0 {
			return raw, true
		}
		offset += idx + len(pattern)
	}
	return nil, false
}

func skipJSONSpace(body []byte, pos int) int {
	for pos < len(body) {
		switch body[pos] {
		case ' ', '\n', '\r', '\t':
			pos++
		default:
			return pos
		}
	}
	return pos
}

// decodeJSON decodes the first JSON value from data, tolerating trailing non-JSON bytes.
//
// It tries json.Unmarshal first, which operates directly on the input slice and
// avoids the Decoder's internal buffer-growth allocations (significant for large
// non-streaming response samples). When the input contains trailing non-JSON
// bytes after a valid top-level value, Unmarshal returns a SyntaxError and the
// call falls back to json.Decoder, which ignores trailing data.
func decodeJSON(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err == nil {
		return nil
	}
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

func stripSSEPrefixBytes(data []byte) []byte {
	data = bytes.TrimSpace(data)
	for bytes.HasPrefix(data, []byte("data:")) {
		data = bytes.TrimSpace(data[len("data:"):])
	}
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return nil
	}
	return data
}

func jsonObjectValid(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '{' && json.Valid(data)
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
	if resp == nil {
		return nil, nil
	}
	model := geminiResponseModel(resp)
	imageCount := countGeminiImageOutputs(resp)
	if len(resp.UsageMetadata) == 0 {
		if imageCount > 0 {
			return &UsageInfo{Model: model, ImageCount: imageCount, Operation: "generation"}, nil
		}
		return nil, nil
	}
	var usage geminiUsage
	if err := json.Unmarshal(resp.UsageMetadata, &usage); err != nil {
		return nil, err
	}
	if !hasGeminiUsageTokens(&usage) && imageCount == 0 {
		return nil, nil
	}
	info := geminiUsageToInfo(model, &usage)
	if imageCount > 0 {
		info.ImageCount = imageCount
		info.Operation = "generation"
	}
	info.UsageRawJSON = rawUsageString(resp.UsageMetadata)
	return info, nil
}

func geminiResponseModel(resp *geminiResponse) string {
	if resp == nil {
		return ""
	}
	if resp.ModelVersion != "" {
		return resp.ModelVersion
	}
	return resp.Model
}

func geminiSampleToInfo(body []byte) (*UsageInfo, bool) {
	model := findJSONStringField(body, "modelVersion")
	if model == "" {
		model = findJSONStringField(body, "model")
	}
	imageCount := countJSONStringFieldPrefix(body, "mimeType", "image/")
	info := &UsageInfo{Model: model, ImageCount: imageCount}
	if raw, ok := findJSONFieldRaw(body, "usageMetadata"); ok {
		var usage geminiUsage
		if err := json.Unmarshal(raw, &usage); err == nil {
			info = geminiUsageToInfo(model, &usage)
			info.ImageCount = imageCount
			info.UsageRawJSON = rawUsageString(raw)
		}
	}
	if imageCount > 0 {
		info.Operation = "generation"
	}
	if info.ImageCount == 0 && info.InputTokens == 0 && info.OutputTokens == 0 && info.TotalTokens == 0 {
		return nil, false
	}
	return info, true
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
	Model  string                `json:"model"`
	Usage  json.RawMessage       `json:"usage"`
	Output []responsesOutputItem `json:"output"`
}

type responsesOutputItem struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

type responsesUsage struct {
	InputTokens         int64                `json:"input_tokens"`
	OutputTokens        int64                `json:"output_tokens"`
	TotalTokens         int64                `json:"total_tokens"`
	InputTokensDetails  *inputTokensDetails  `json:"input_tokens_details"`
	OutputTokensDetails *outputTokensDetails `json:"output_tokens_details"`
}

type inputTokensDetails struct {
	CachedTokens      int64 `json:"cached_tokens"`
	TextTokens        int64 `json:"text_tokens"`
	ImageTokens       int64 `json:"image_tokens"`
	CachedTextTokens  int64 `json:"cached_text_tokens"`
	CachedImageTokens int64 `json:"cached_image_tokens"`
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
	Model         string            `json:"model"`
	ModelVersion  string            `json:"modelVersion"`
	UsageMetadata json.RawMessage   `json:"usageMetadata"`
	Candidates    []geminiCandidate `json:"candidates"`
}

type geminiUsage struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
	ToolUsePromptTokenCount int64 `json:"toolUsePromptTokenCount"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	InlineData      *geminiInlineData `json:"inlineData"`
	InlineDataSnake *geminiInlineData `json:"inline_data"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
}

type imageUsage struct {
	InputTokens        int64               `json:"input_tokens"`
	OutputTokens       int64               `json:"output_tokens"`
	TotalTokens        int64               `json:"total_tokens"`
	InputTokensDetails *inputTokensDetails `json:"input_tokens_details"`
}

func countResponsesImageOutputs(items []responsesOutputItem) int64 {
	var count int64
	for _, item := range items {
		if item.Type == "image_generation_call" {
			count++
		}
	}
	return count
}

func countGeminiImageOutputs(resp *geminiResponse) int64 {
	if resp == nil {
		return 0
	}
	var count int64
	for _, candidate := range resp.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil && isImageMIME(part.InlineData.MimeType) {
				count++
			}
			if part.InlineDataSnake != nil && isImageMIME(part.InlineDataSnake.MimeType) {
				count++
			}
		}
	}
	return count
}

func isImageMIME(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "image/")
}
