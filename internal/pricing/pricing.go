package pricing

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Pricing struct {
	Models map[string]ModelPrice `yaml:"pricing"`
	// aliasIndex maps alias names to their canonical model key.
	aliasIndex map[string]string
}

type ModelPrice struct {
	InputPer1M       float64  `yaml:"input_per_1m"`
	CachedInputPer1M float64  `yaml:"cached_input_per_1m"`
	OutputPer1M      float64  `yaml:"output_per_1m"`
	ReasoningPer1M   float64  `yaml:"reasoning_per_1m"`
	Aliases          []string `yaml:"aliases"`
}

// NewPricing returns an empty Pricing with initialized index.
func NewPricing() *Pricing {
	return &Pricing{
		Models:     make(map[string]ModelPrice),
		aliasIndex: make(map[string]string),
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
	mp, ok := p.lookup(model)
	if !ok {
		return 0, false
	}
	return computeCost(mp, inputTokens, outputTokens, reasoningTokens, cachedTokens), true
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

func computeCost(mp ModelPrice, inputTokens, outputTokens, reasoningTokens, cachedTokens int64) float64 {
	cost := 0.0

	if cachedTokens > 0 {
		cost += float64(cachedTokens) / 1_000_000.0 * mp.CachedInputPer1M
	}
	nonCachedInput := inputTokens - cachedTokens
	if nonCachedInput > 0 {
		cost += float64(nonCachedInput) / 1_000_000.0 * mp.InputPer1M
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
