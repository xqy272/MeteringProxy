package pricing

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Pricing struct {
	Models     map[string]ModelPrice           `yaml:"pricing"`
	Multimodal map[string]MultimodalModelPrice `yaml:"multimodal_pricing"`
	// aliasIndex maps alias names to their canonical model key.
	aliasIndex           map[string]string
	multimodalAliasIndex map[string]string
}

type ModelPrice struct {
	InputPer1M         float64  `yaml:"input_per_1m"`
	CachedInputPer1M   float64  `yaml:"cached_input_per_1m"`
	OutputPer1M        float64  `yaml:"output_per_1m"`
	ReasoningPer1M     float64  `yaml:"reasoning_per_1m"`
	CacheCreationPer1M float64  `yaml:"cache_creation_per_1m"`
	Aliases            []string `yaml:"aliases"`
}

type MultimodalModelPrice struct {
	Text         ModalityPrice `yaml:"text"`
	Image        ModalityPrice `yaml:"image"`
	Audio        ModalityPrice `yaml:"audio"`
	AudioSeconds ModalityPrice `yaml:"audio_seconds"`
	Aliases      []string      `yaml:"aliases"`
}

type ModalityPrice struct {
	InputPer1M       float64 `yaml:"input_per_1m"`
	CachedInputPer1M float64 `yaml:"cached_input_per_1m"`
	OutputPer1M      float64 `yaml:"output_per_1m"`
	ReasoningPer1M   float64 `yaml:"reasoning_per_1m"`
	PerSecond        float64 `yaml:"per_second"`
	InputPerSecond   float64 `yaml:"input_per_second"`
	OutputPerSecond  float64 `yaml:"output_per_second"`
	PerMinute        float64 `yaml:"per_minute"`
	InputPerMinute   float64 `yaml:"input_per_minute"`
	OutputPerMinute  float64 `yaml:"output_per_minute"`
}

// NewPricing returns an empty Pricing with initialized index.
func NewPricing() *Pricing {
	return &Pricing{
		Models:               make(map[string]ModelPrice),
		Multimodal:           make(map[string]MultimodalModelPrice),
		aliasIndex:           make(map[string]string),
		multimodalAliasIndex: make(map[string]string),
	}
}

func Load(path string) (*Pricing, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pricing: %w", err)
	}
	p := &Pricing{}
	if err := yaml.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("parse pricing: %w", err)
	}
	if p.Models == nil {
		p.Models = make(map[string]ModelPrice)
	}
	if p.Multimodal == nil {
		p.Multimodal = make(map[string]MultimodalModelPrice)
	}
	p.buildAliasIndex()
	return p, nil
}

func (p *Pricing) buildAliasIndex() {
	p.aliasIndex = make(map[string]string)
	for name, mp := range p.Models {
		for _, alias := range mp.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				p.aliasIndex[alias] = name
			}
		}
	}
	p.multimodalAliasIndex = make(map[string]string)
	for name, mp := range p.Multimodal {
		for _, alias := range mp.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				p.multimodalAliasIndex[alias] = name
			}
		}
	}
}

// Cost computes the cost for a given model. The matching chain is:
//  1. exact model name match
//  2. explicit alias match (configured in pricing.yaml)
//  3. canonicalized model name match
//  4. unknown (cost = 0, known = false)
//
// Canonicalization strips known provider date tags (e.g., "-2026-03-17").
// It is only used for pricing lookup and never alters the persisted model name.
func (p *Pricing) Cost(model string, inputTokens, outputTokens, reasoningTokens, cachedTokens int64) (float64, bool) {
	return p.CostWithCacheCreation(model, inputTokens, outputTokens, reasoningTokens, cachedTokens, 0)
}

func (p *Pricing) CostWithCacheCreation(model string, inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheCreationTokens int64) (float64, bool) {
	mp, ok := p.lookup(model)
	if !ok {
		return 0, false
	}
	return computeCost(mp, inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheCreationTokens), true
}

func (p *Pricing) CostDimension(model, modality, channel, metric, direction, unit string, amount float64) (float64, bool) {
	if amount <= 0 {
		return 0, true
	}
	mp, ok := p.lookupMultimodal(model)
	if !ok {
		return 0, false
	}
	if metric == "tokens" && unit == "token" {
		price, ok := modalityTokenPrice(mp, modality, channel)
		if !ok {
			return 0, false
		}
		rate, ok := tokenDirectionRate(price, direction)
		if !ok || rate <= 0 {
			return 0, false
		}
		return amount / 1_000_000.0 * rate, true
	}
	if metric == "seconds" && unit == "second" {
		price := mp.AudioSeconds
		rate := secondsRate(price.PerSecond, price.PerMinute)
		switch direction {
		case "input":
			if inputRate := secondsRate(price.InputPerSecond, price.InputPerMinute); inputRate > 0 {
				rate = inputRate
			}
		case "output":
			if outputRate := secondsRate(price.OutputPerSecond, price.OutputPerMinute); outputRate > 0 {
				rate = outputRate
			}
		}
		if rate <= 0 {
			return 0, false
		}
		return amount * rate, true
	}
	return 0, false
}

func secondsRate(perSecond, perMinute float64) float64 {
	if perSecond > 0 {
		return perSecond
	}
	if perMinute > 0 {
		return perMinute / 60
	}
	return 0
}

func (p *Pricing) lookup(model string) (ModelPrice, bool) {
	// 1. Exact match.
	if mp, ok := p.Models[model]; ok {
		return mp, true
	}

	// 2. Explicit alias match.
	if canonical, ok := p.aliasIndex[model]; ok {
		if mp, ok := p.Models[canonical]; ok {
			return mp, true
		}
	}

	// 3. Canonicalized form match.
	canonical := canonicalize(model)
	if canonical != model {
		if mp, ok := p.Models[canonical]; ok {
			return mp, true
		}
	}

	return ModelPrice{}, false
}

func (p *Pricing) lookupMultimodal(model string) (MultimodalModelPrice, bool) {
	if mp, ok := p.Multimodal[model]; ok {
		return mp, true
	}
	if canonical, ok := p.multimodalAliasIndex[model]; ok {
		if mp, ok := p.Multimodal[canonical]; ok {
			return mp, true
		}
	}
	canonical := canonicalize(model)
	if canonical != model {
		if mp, ok := p.Multimodal[canonical]; ok {
			return mp, true
		}
	}
	return MultimodalModelPrice{}, false
}

func modalityTokenPrice(mp MultimodalModelPrice, modality, channel string) (ModalityPrice, bool) {
	switch channel {
	case "text":
		return mp.Text, true
	case "image":
		return mp.Image, true
	case "audio":
		return mp.Audio, true
	}
	switch modality {
	case "text":
		return mp.Text, true
	case "image":
		return mp.Image, true
	case "audio":
		return mp.Audio, true
	}
	return ModalityPrice{}, false
}

func tokenDirectionRate(price ModalityPrice, direction string) (float64, bool) {
	switch direction {
	case "input":
		return price.InputPer1M, true
	case "cached_input":
		return price.CachedInputPer1M, true
	case "output":
		return price.OutputPer1M, true
	case "reasoning":
		return price.ReasoningPer1M, true
	}
	return 0, false
}

// canonicalize strips provider-specific version suffixes from model names.
// This is only used for pricing lookup. The stored model_returned is never altered.
//
// Rules:
//   - OpenAI style: "gpt-4o-2026-03-18" -> "gpt-4o" (strip trailing -YYYY-MM-DD)
//   - Anthropic style: "claude-sonnet-4-6-20250514" -> "claude-sonnet-4-6" (strip trailing -YYYYMMDD)
//   - DeepSeek style: "deepseek-chat-2026-05-01" -> "deepseek-chat" (strip trailing -YYYY-MM-DD)
func canonicalize(model string) string {
	// Strip trailing -YYYY-MM-DD (OpenAI, DeepSeek style: 10 chars including leading dash).
	if len(model) > 11 {
		suffix := model[len(model)-11:] // "-2026-03-18" = 11 chars
		if isDateSuffix(suffix) {
			return model[:len(model)-11]
		}
	}

	// Strip trailing -YYYYMMDD (Anthropic style: 9 chars including leading dash).
	if len(model) > 9 {
		suffix := model[len(model)-9:] // "-20250514" = 9 chars
		if isCompactDateSuffix(suffix) {
			return model[:len(model)-9]
		}
	}

	return model
}

// isDateSuffix checks if a string matches "-YYYY-MM-DD" format.
func isDateSuffix(s string) bool {
	if len(s) != 11 || s[0] != '-' {
		return false
	}
	for i := 1; i < 11; i++ {
		c := s[i]
		if i == 5 || i == 8 {
			if c != '-' {
				return false
			}
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isCompactDateSuffix checks if a string matches "-YYYYMMDD" format.
func isCompactDateSuffix(s string) bool {
	if len(s) != 9 || s[0] != '-' {
		return false
	}
	for i := 1; i < 9; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func computeCost(mp ModelPrice, inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheCreationTokens int64) float64 {
	cost := 0.0

	nonCachedInput, cachedTokens, cacheCreationTokens := inputBreakdown(inputTokens, cachedTokens, cacheCreationTokens)

	if cachedTokens > 0 {
		cost += float64(cachedTokens) / 1_000_000.0 * mp.CachedInputPer1M
	}
	if nonCachedInput > 0 {
		cost += float64(nonCachedInput) / 1_000_000.0 * mp.InputPer1M
	}
	if cacheCreationTokens > 0 && mp.CacheCreationPer1M > 0 {
		cost += float64(cacheCreationTokens) / 1_000_000.0 * mp.CacheCreationPer1M
	} else if cacheCreationTokens > 0 {
		cost += float64(cacheCreationTokens) / 1_000_000.0 * mp.InputPer1M
	}

	if reasoningTokens > 0 && mp.ReasoningPer1M > 0 {
		regularOutput := outputTokens - reasoningTokens
		if regularOutput > 0 {
			cost += float64(regularOutput) / 1_000_000.0 * mp.OutputPer1M
		}
		cost += float64(reasoningTokens) / 1_000_000.0 * mp.ReasoningPer1M
	} else {
		cost += float64(outputTokens) / 1_000_000.0 * mp.OutputPer1M
	}

	return cost
}

func inputBreakdown(inputTokens, cachedTokens, cacheCreationTokens int64) (nonCachedInput, cachedInput, cacheCreationInput int64) {
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cacheCreationTokens < 0 {
		cacheCreationTokens = 0
	}
	if inputTokens <= 0 {
		return 0, cachedTokens, cacheCreationTokens
	}
	if cacheCreationTokens > inputTokens {
		cacheCreationTokens = inputTokens
	}
	remaining := inputTokens - cacheCreationTokens
	if cachedTokens > remaining {
		cachedTokens = remaining
	}
	return remaining - cachedTokens, cachedTokens, cacheCreationTokens
}
