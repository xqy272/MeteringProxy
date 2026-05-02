package pricing

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Pricing struct {
	Models map[string]ModelPrice `yaml:"pricing"`
}

type ModelPrice struct {
	InputPer1M       float64 `yaml:"input_per_1m"`
	CachedInputPer1M float64 `yaml:"cached_input_per_1m"`
	OutputPer1M      float64 `yaml:"output_per_1m"`
	ReasoningPer1M   float64 `yaml:"reasoning_per_1m"`
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
	return p, nil
}

func (p *Pricing) Cost(model string, inputTokens, outputTokens, reasoningTokens, cachedTokens int64) (float64, bool) {
	mp, ok := p.Models[model]
	if !ok {
		return 0, false
	}
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

	return cost, true
}