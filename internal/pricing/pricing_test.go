package pricing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePricing(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pricing.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write pricing: %v", err)
	}
	return path
}

func TestCost_NoReasoning(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  gpt-4o:
    input_per_1m: 2.50
    cached_input_per_1m: 1.25
    output_per_1m: 10.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cost, known := p.Cost("gpt-4o", 100, 50, 0, 0)
	if !known {
		t.Fatal("expected known cost")
	}
	expected := 100/1_000_000.0*2.50 + 50/1_000_000.0*10.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f", cost, expected)
	}
}

func TestCost_UnknownModel(t *testing.T) {
	p, _ := Load(writePricing(t, `pricing: {}`))
	cost, known := p.Cost("unknown-model", 100, 50, 0, 0)
	if known {
		t.Error("expected unknown model")
	}
	if cost != 0 {
		t.Errorf("cost = %f, want 0", cost)
	}
}

func TestCost_ReasoningIncludedInOutput_NoSeparatePrice(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  deepseek-chat:
    input_per_1m: 0.27
    cached_input_per_1m: 0.07
    output_per_1m: 1.10
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cost, known := p.Cost("deepseek-chat", 500, 1000, 200, 0)
	if !known {
		t.Fatal("expected known cost")
	}

	expected := 500/1_000_000.0*0.27 + 1000/1_000_000.0*1.10
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f (diff=%.6f)", cost, expected, diff)
	}
}

func TestCost_ReasoningWithSeparatePrice_NoDoubleBilling(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  claude-sonnet-4-6:
    input_per_1m: 3.00
    cached_input_per_1m: 0.30
    output_per_1m: 15.00
    reasoning_per_1m: 3.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cost, known := p.Cost("claude-sonnet-4-6", 200, 1000, 200, 50)
	if !known {
		t.Fatal("expected known cost")
	}

	expected := 150/1_000_000.0*3.00 + 50/1_000_000.0*0.30 + 800/1_000_000.0*15.00 + 200/1_000_000.0*3.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f (diff=%.6f)", cost, expected, diff)
	}

	wrongCost := 150/1_000_000.0*3.00 + 50/1_000_000.0*0.30 + 1000/1_000_000.0*15.00 + 200/1_000_000.0*3.00
	if cost == wrongCost {
		t.Error("cost matches naive double-billing formula; likely double-counting")
	}
}

func TestCost_CachedInputDoesNotExceedInput(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  gpt-4o:
    input_per_1m: 2.50
    cached_input_per_1m: 1.25
    output_per_1m: 10.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cost, known := p.Cost("gpt-4o", 1000, 50, 0, 600)
	if !known {
		t.Fatal("expected known cost")
	}

	expected := 400/1_000_000.0*2.50 + 600/1_000_000.0*1.25 + 50/1_000_000.0*10.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f", cost, expected)
	}
}

func TestCost_CacheCreationUsesSeparatePrice(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  claude-sonnet-4-6:
    input_per_1m: 3.00
    cached_input_per_1m: 0.30
    cache_creation_per_1m: 3.75
    output_per_1m: 15.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cost, known := p.CostWithCacheCreation("claude-sonnet-4-6", 125, 30, 0, 20, 5)
	if !known {
		t.Fatal("expected known cost")
	}

	expected := 100/1_000_000.0*3.00 + 20/1_000_000.0*0.30 + 5/1_000_000.0*3.75 + 30/1_000_000.0*15.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f", cost, expected)
	}
}

func TestCost_CacheCreationFallsBackToInputPrice(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  claude-sonnet-4-6:
    input_per_1m: 3.00
    cached_input_per_1m: 0.30
    output_per_1m: 15.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cost, known := p.CostWithCacheCreation("claude-sonnet-4-6", 5, 0, 0, 0, 5)
	if !known {
		t.Fatal("expected known cost")
	}
	expected := 5 / 1_000_000.0 * 3.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("cost = %.6f, want %.6f", cost, expected)
	}
}

func TestCost_ChargesExplicitCacheTokensWhenInputMissing(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  recovered:
    input_per_1m: 2.00
    cached_input_per_1m: 0.20
    cache_creation_per_1m: 2.50
    output_per_1m: 4.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cost, known := p.CostWithCacheCreation("recovered", 0, 0, 0, 7, 5)
	if !known {
		t.Fatal("expected known cost")
	}
	expected := 7/1_000_000.0*0.20 + 5/1_000_000.0*2.50
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("cost = %.6f, want %.6f", cost, expected)
	}
}

func TestCost_CacheBreakdownClampedToInput(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  gemini-test:
    input_per_1m: 2.00
    cached_input_per_1m: 0.20
    cache_creation_per_1m: 2.50
    output_per_1m: 4.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cost, known := p.CostWithCacheCreation("gemini-test", 10, 5, 0, 50, 5)
	if !known {
		t.Fatal("expected known cost")
	}

	expected := 5/1_000_000.0*0.20 + 5/1_000_000.0*2.50 + 5/1_000_000.0*4.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f", cost, expected)
	}
}

func TestCost_ReasoningExceedsOutput(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  test:
    input_per_1m: 1.00
    cached_input_per_1m: 0.50
    output_per_1m: 5.00
    reasoning_per_1m: 1.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cost, known := p.Cost("test", 100, 200, 300, 0)
	if !known {
		t.Fatal("expected known cost")
	}

	expected := 100/1_000_000.0*1.00 + 0 + 300/1_000_000.0*1.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f", cost, expected)
	}
}

// W6: Alias tests

func TestCost_AliasMatch(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  gpt-4o:
    input_per_1m: 2.50
    cached_input_per_1m: 1.25
    output_per_1m: 10.00
    aliases:
      - gpt-4o-latest
      - gpt-4o-nightly
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Exact match still works.
	cost, known := p.Cost("gpt-4o", 100, 50, 0, 0)
	if !known {
		t.Error("exact match should work")
	}
	expected := 100/1_000_000.0*2.50 + 50/1_000_000.0*10.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("exact cost = %.6f, want %.6f", cost, expected)
	}

	// Alias match.
	cost2, known2 := p.Cost("gpt-4o-latest", 100, 50, 0, 0)
	if !known2 {
		t.Error("alias match should be known")
	}
	if cost2 != cost {
		t.Errorf("alias cost = %.6f, want %.6f (same as exact)", cost2, cost)
	}

	// Second alias.
	cost3, known3 := p.Cost("gpt-4o-nightly", 100, 50, 0, 0)
	if !known3 {
		t.Error("second alias should be known")
	}
	if cost3 != cost {
		t.Errorf("second alias cost = %.6f, want %.6f", cost3, cost)
	}
}

func TestCost_Canonicalization_OpenAIDateSuffix(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  gpt-4o:
    input_per_1m: 2.50
    cached_input_per_1m: 1.25
    output_per_1m: 10.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// gpt-4o-2026-03-18 should canonicalize to gpt-4o
	cost, known := p.Cost("gpt-4o-2026-03-18", 100, 50, 0, 0)
	if !known {
		t.Error("canonicalized OpenAI date-suffix model should be known")
	}
	expected := 100/1_000_000.0*2.50 + 50/1_000_000.0*10.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("canonicalized cost = %.6f, want %.6f", cost, expected)
	}
}

func TestCost_Canonicalization_AnthropicCompactDate(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  claude-sonnet-4-6:
    input_per_1m: 3.00
    cached_input_per_1m: 0.30
    output_per_1m: 15.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// claude-sonnet-4-6-20250514 should canonicalize to claude-sonnet-4-6
	_, known := p.Cost("claude-sonnet-4-6-20250514", 100, 50, 0, 0)
	if !known {
		t.Error("canonicalized Anthropic date-suffix model should be known")
	}
}

func TestCost_NoImplicitPrefixMatch(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  gpt-4o:
    input_per_1m: 2.50
    cached_input_per_1m: 1.25
    output_per_1m: 10.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// gpt-4 (prefix of gpt-4o) should NOT match
	_, known := p.Cost("gpt-4", 100, 50, 0, 0)
	if known {
		t.Error("gpt-4 should NOT be a prefix match for gpt-4o; no implicit matching allowed")
	}

	// gpt-4o-mini should NOT match gpt-4o
	_, known = p.Cost("gpt-4o-mini", 100, 50, 0, 0)
	if known {
		t.Error("gpt-4o-mini should NOT match gpt-4o; no implicit prefix matching")
	}
}

func TestCost_NoImplicitSuffixTruncation(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  gpt-4o:
    input_per_1m: 2.50
    cached_input_per_1m: 1.25
    output_per_1m: 10.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// gpt-4o-abc should NOT match (not a date suffix)
	_, known := p.Cost("gpt-4o-abc", 100, 50, 0, 0)
	if known {
		t.Error("gpt-4o-abc should NOT match; -abc is not a date suffix")
	}
}

func TestCost_AliasTakesPriorityOverCanonical(t *testing.T) {
	p, err := Load(writePricing(t, `
pricing:
  gpt-4o:
    input_per_1m: 2.50
    cached_input_per_1m: 1.25
    output_per_1m: 10.00
  gpt-4o-special:
    input_per_1m: 5.00
    cached_input_per_1m: 2.50
    output_per_1m: 20.00
    aliases:
      - gpt-4o-2026-03-18
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// gpt-4o-2026-03-18 is an explicit alias for gpt-4o-special.
	// The alias should take priority over canonicalization to gpt-4o.
	cost, known := p.Cost("gpt-4o-2026-03-18", 100, 50, 0, 0)
	if !known {
		t.Fatal("expected known cost")
	}
	// Should use gpt-4o-special pricing (5.00/20.00), not gpt-4o (2.50/10.00)
	expectedSpecial := 100/1_000_000.0*5.00 + 50/1_000_000.0*20.00
	expectedRegular := 100/1_000_000.0*2.50 + 50/1_000_000.0*10.00
	if diff := cost - expectedSpecial; diff < -0.0001 || diff > 0.0001 {
		if diff2 := cost - expectedRegular; diff2 < -0.0001 || diff2 > 0.0001 {
			t.Errorf("cost = %.6f, want special alias cost %.6f; alias should take priority over canonicalization",
				cost, expectedSpecial)
		} else {
			t.Error("alias did not take priority; canonicalization matched first")
		}
	}
}

func TestNewPricing_EmptyButSafe(t *testing.T) {
	p := NewPricing()
	if p.Models == nil {
		t.Error("Models map should be initialized")
	}
	_, known := p.Cost("any-model", 100, 50, 0, 0)
	if known {
		t.Error("empty pricing should not match any model")
	}
}

func TestCostDimension_GPTImage2(t *testing.T) {
	p, err := Load(writePricing(t, `
multimodal_pricing:
  gpt-image-2:
    text:
      input_per_1m: 5.00
      cached_input_per_1m: 1.25
    image:
      input_per_1m: 8.00
      cached_input_per_1m: 2.00
      output_per_1m: 30.00
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	textCost, known := p.CostDimension("gpt-image-2", "image", "text", "tokens", "input", "token", 10)
	if !known {
		t.Fatal("text image prompt tokens should be priced")
	}
	imageInCost, known := p.CostDimension("gpt-image-2", "image", "image", "tokens", "input", "token", 40)
	if !known {
		t.Fatal("input image tokens should be priced")
	}
	imageOutCost, known := p.CostDimension("gpt-image-2", "image", "image", "tokens", "output", "token", 50)
	if !known {
		t.Fatal("output image tokens should be priced")
	}
	expected := 10/1_000_000.0*5.00 + 40/1_000_000.0*8.00 + 50/1_000_000.0*30.00
	got := textCost + imageInCost + imageOutCost
	if diff := got - expected; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("cost = %.6f, want %.6f", got, expected)
	}
}

func TestCostDimension_RealtimeSeconds(t *testing.T) {
	p, err := Load(writePricing(t, `
multimodal_pricing:
  gpt-realtime-translate:
    audio_seconds:
      per_second: 0.00057
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cost, known := p.CostDimension("gpt-realtime-translate", "audio", "audio", "seconds", "input", "second", 60)
	if !known {
		t.Fatal("audio seconds should be priced")
	}
	expected := 60 * 0.00057
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("cost = %.6f, want %.6f", cost, expected)
	}
}

func TestCostDimension_RealtimeSecondsFromPerMinute(t *testing.T) {
	p, err := Load(writePricing(t, `
multimodal_pricing:
  gpt-realtime-translate:
    audio_seconds:
      input_per_minute: 0.0342
      output_per_minute: 0.0684
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	inputCost, known := p.CostDimension("gpt-realtime-translate", "audio", "audio", "seconds", "input", "second", 30)
	if !known {
		t.Fatal("input audio minutes should be priced")
	}
	outputCost, known := p.CostDimension("gpt-realtime-translate", "audio", "audio", "seconds", "output", "second", 15)
	if !known {
		t.Fatal("output audio minutes should be priced")
	}
	expected := 30/60.0*0.0342 + 15/60.0*0.0684
	got := inputCost + outputCost
	if diff := got - expected; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("cost = %.6f, want %.6f", got, expected)
	}
}

func TestCanonicalize(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"gpt-4o", "gpt-4o"},
		{"gpt-4o-2026-03-18", "gpt-4o"},
		{"gpt-5.4-mini-2026-03-17", "gpt-5.4-mini"},
		{"claude-sonnet-4-6-20250514", "claude-sonnet-4-6"},
		{"claude-opus-4-7", "claude-opus-4-7"},
		{"deepseek-chat", "deepseek-chat"},
		{"deepseek-chat-2026-05-01", "deepseek-chat"},
		{"", ""},
		{"gpt-4o-abc", "gpt-4o-abc"}, // not a date suffix
		{"gpt-4o-20", "gpt-4o-20"},   // too short for date suffix
	}
	for _, tc := range tests {
		got := canonicalize(tc.input)
		if got != tc.want {
			t.Errorf("canonicalize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLoad_DefaultPricingYAML(t *testing.T) {
	root := filepath.Join("..", "..", "pricing.yaml")
	p, err := Load(root)
	if err != nil {
		t.Fatalf("Load default pricing.yaml: %v", err)
	}

	// DeepSeek aliases
	if _, known := p.Cost("ds-openai-flash", 1000, 1000, 0, 0); !known {
		t.Fatal("ds-openai-flash alias should resolve")
	}
	if _, known := p.Cost("ds-openai-pro", 1000, 1000, 0, 0); !known {
		t.Fatal("ds-openai-pro alias should resolve")
	}

	// grok-4.5 flat low tier, no long_context
	mp, ok := p.Models["grok-4.5"]
	if !ok {
		t.Fatal("grok-4.5 missing from default pricing.yaml")
	}
	if mp.InputPer1M != 2.00 || mp.CachedInputPer1M != 0.30 || mp.OutputPer1M != 6.00 {
		t.Fatalf("grok-4.5 prices = %+v", mp)
	}
	if mp.LongContext != nil {
		t.Fatal("grok-4.5 must not include long_context")
	}

	// Gemini uses the confirmed request-level 200k long-context tier.
	g, ok := p.Models["gemini-3.1-pro-preview"]
	if !ok {
		t.Fatal("gemini-3.1-pro-preview missing")
	}
	if g.LongContext == nil {
		t.Fatal("default pricing.yaml must enable Gemini long_context")
	}
	if g.LongContext.ThresholdInputTokens != 200000 ||
		g.LongContext.InputPer1M != 4.00 || g.LongContext.CachedInputPer1M != 0.40 ||
		g.LongContext.OutputPer1M != 18.00 {
		t.Fatalf("Gemini long_context prices = %+v", g.LongContext)
	}
	shortCost, known := p.CostText("gemini-3.1-pro-preview-customtools", TextTokenUsage{
		InputTokens: 199999, OutputTokens: 1000, RequestInputTokens: 199999,
	})
	if !known {
		t.Fatal("Gemini short tier alias should resolve")
	}
	longCost, known := p.CostText("gemini-3.1-pro-preview-customtools", TextTokenUsage{
		InputTokens: 200000, OutputTokens: 1000, RequestInputTokens: 200000,
	})
	if !known || longCost <= shortCost {
		t.Fatalf("Gemini tier costs short=%f long=%f known=%v", shortCost, longCost, known)
	}

	// Imagine per-image
	cost, known, defaulted := p.CostImages("grok-imagine-image", 2, 1, "1024x1024")
	if !known {
		t.Fatal("grok-imagine-image alias should price images")
	}
	if defaulted {
		t.Fatal("1024x1024 should not default")
	}
	expected := 2*0.01 + 1*0.05
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("imagine cost = %.6f, want %.6f", cost, expected)
	}
	cost2, known, defaulted := p.CostImages("grok-imagine-image-quality", 0, 1, "2048x2048")
	if !known || defaulted {
		t.Fatalf("2K image known=%v defaulted=%v", known, defaulted)
	}
	if diff := cost2 - 0.07; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("2K cost = %.6f, want 0.07", cost2)
	}
}

func TestParse_RejectsUnknownField(t *testing.T) {
	_, err := Parse([]byte(`
pricing:
  gpt-4o:
    input_per_1m: 1.0
    output_per_1m: 2.0
    unknown_field: 1
`))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown_field") && !strings.Contains(strings.ToLower(err.Error()), "field") {
		t.Fatalf("error should mention unknown field: %v", err)
	}
}

func TestParse_RejectsMultipleDocuments(t *testing.T) {
	_, err := Parse([]byte(`
pricing:
  gpt-4o:
    input_per_1m: 1.0
    output_per_1m: 2.0
---
pricing: {}
`))
	if err == nil {
		t.Fatal("expected multi-document error")
	}
	if !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParse_RejectsNegativePrice(t *testing.T) {
	_, err := Parse([]byte(`
pricing:
  gpt-4o:
    input_per_1m: -1
    output_per_1m: 2.0
`))
	if err == nil {
		t.Fatal("expected negative price error")
	}
	if !strings.Contains(err.Error(), "pricing.gpt-4o.input_per_1m") {
		t.Fatalf("error path missing: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "password") || strings.Contains(strings.ToLower(err.Error()), "secret") {
		t.Fatalf("error must not include credentials: %v", err)
	}
}

func TestParse_RejectsInvalidLongContextThreshold(t *testing.T) {
	_, err := Parse([]byte(`
pricing:
  gemini-3.1-pro-preview:
    input_per_1m: 2.0
    output_per_1m: 12.0
    long_context:
      threshold_input_tokens: 0
      input_per_1m: 4.0
      output_per_1m: 18.0
`))
	if err == nil {
		t.Fatal("expected threshold error")
	}
	if !strings.Contains(err.Error(), "threshold_input_tokens") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParse_RejectsDuplicateAlias(t *testing.T) {
	_, err := Parse([]byte(`
pricing:
  a:
    input_per_1m: 1
    output_per_1m: 1
    aliases: [shared]
  b:
    input_per_1m: 2
    output_per_1m: 2
    aliases: [shared]
`))
	if err == nil {
		t.Fatal("expected duplicate alias error")
	}
	if !strings.Contains(err.Error(), "duplicate alias") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParse_RejectsAliasCanonicalConflict(t *testing.T) {
	_, err := Parse([]byte(`
pricing:
  a:
    input_per_1m: 1
    output_per_1m: 1
  b:
    input_per_1m: 2
    output_per_1m: 2
    aliases: [a]
`))
	if err == nil {
		t.Fatal("expected alias/canonical conflict")
	}
	if !strings.Contains(err.Error(), "conflicts with canonical") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParse_RejectsPerImageOutputMissingDefault(t *testing.T) {
	_, err := Parse([]byte(`
multimodal_pricing:
  img:
    image:
      per_image_output:
        "1K": 0.05
`))
	if err == nil {
		t.Fatal("expected missing default error")
	}
	if !strings.Contains(err.Error(), "default") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParse_RejectsUnsupportedResolutionKey(t *testing.T) {
	_, err := Parse([]byte(`
multimodal_pricing:
  img:
    image:
      per_image_output:
        default: 0.05
        "4K": 0.20
`))
	if err == nil {
		t.Fatal("expected unsupported key error")
	}
	if !strings.Contains(err.Error(), "unsupported resolution key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func geminiTierFixture(t *testing.T) *Pricing {
	t.Helper()
	p, err := Parse([]byte(`
pricing:
  gemini-3.1-pro-preview:
    input_per_1m: 2.00
    cached_input_per_1m: 0.20
    output_per_1m: 12.00
    cache_creation_per_1m: 2.50
    reasoning_per_1m: 1.00
    long_context:
      threshold_input_tokens: 200000
      input_per_1m: 4.00
      cached_input_per_1m: 0.40
      output_per_1m: 18.00
      cache_creation_per_1m: 5.00
      reasoning_per_1m: 2.00
`))
	if err != nil {
		t.Fatalf("Parse fixture: %v", err)
	}
	return p
}

func TestCostText_LongContextThresholdBoundaries(t *testing.T) {
	p := geminiTierFixture(t)
	const threshold int64 = 200000

	// threshold-1 => short
	cost, known := p.CostText("gemini-3.1-pro-preview", TextTokenUsage{
		InputTokens:        threshold - 1,
		OutputTokens:       1000,
		RequestInputTokens: threshold - 1,
	})
	if !known {
		t.Fatal("expected known")
	}
	wantShort := float64(threshold-1)/1_000_000.0*2.00 + 1000/1_000_000.0*12.00
	if diff := cost - wantShort; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("threshold-1 cost=%.8f want=%.8f", cost, wantShort)
	}

	// exact threshold => long
	cost, known = p.CostText("gemini-3.1-pro-preview", TextTokenUsage{
		InputTokens:        threshold,
		OutputTokens:       1000,
		RequestInputTokens: threshold,
	})
	if !known {
		t.Fatal("expected known")
	}
	wantLong := float64(threshold)/1_000_000.0*4.00 + 1000/1_000_000.0*18.00
	if diff := cost - wantLong; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("threshold cost=%.8f want=%.8f", cost, wantLong)
	}

	// threshold+1 => long
	cost, known = p.CostText("gemini-3.1-pro-preview", TextTokenUsage{
		InputTokens:        threshold + 1,
		OutputTokens:       1000,
		RequestInputTokens: threshold + 1,
	})
	if !known {
		t.Fatal("expected known")
	}
	wantLongPlus := float64(threshold+1)/1_000_000.0*4.00 + 1000/1_000_000.0*18.00
	if diff := cost - wantLongPlus; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("threshold+1 cost=%.8f want=%.8f", cost, wantLongPlus)
	}
}

func TestCostText_TwoShortRequestsDoNotBecomeLong(t *testing.T) {
	p := geminiTierFixture(t)
	// Two short requests of 150k each: sum 300k > 200k, but each is short.
	reqA := TextTokenUsage{InputTokens: 150000, OutputTokens: 100, RequestInputTokens: 150000}
	reqB := TextTokenUsage{InputTokens: 150000, OutputTokens: 100, RequestInputTokens: 150000}
	costA, _ := p.CostText("gemini-3.1-pro-preview", reqA)
	costB, _ := p.CostText("gemini-3.1-pro-preview", reqB)
	sum := costA + costB
	wantEach := 150000/1_000_000.0*2.00 + 100/1_000_000.0*12.00
	want := 2 * wantEach
	if diff := sum - want; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("sum=%.8f want=%.8f", sum, want)
	}

	// Aggregate Cost path must remain on base/short prices even with sum > threshold.
	agg, known := p.Cost("gemini-3.1-pro-preview", 300000, 200, 0, 0)
	if !known {
		t.Fatal("expected known")
	}
	wantBase := 300000/1_000_000.0*2.00 + 200/1_000_000.0*12.00
	if diff := agg - wantBase; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("aggregate Cost should use base tier: got=%.8f want=%.8f", agg, wantBase)
	}
	// And must not equal long-tier on the aggregate sum.
	longOnAgg := 300000/1_000_000.0*4.00 + 200/1_000_000.0*18.00
	if agg == longOnAgg {
		t.Fatal("aggregate Cost incorrectly applied long tier")
	}
}

func TestCostText_CachedReasoningCacheCreationTiers(t *testing.T) {
	p := geminiTierFixture(t)

	// short tier with cached + reasoning + cache creation
	short, known := p.CostText("gemini-3.1-pro-preview", TextTokenUsage{
		InputTokens:         1000,
		OutputTokens:        500,
		ReasoningTokens:     100,
		CachedTokens:        200,
		CacheCreationTokens: 50,
		RequestInputTokens:  1000,
	})
	if !known {
		t.Fatal("expected known")
	}
	// non-cached = 1000-50-200 = 750
	wantShort := 750/1_000_000.0*2.00 + 200/1_000_000.0*0.20 + 50/1_000_000.0*2.50 + 400/1_000_000.0*12.00 + 100/1_000_000.0*1.00
	if diff := short - wantShort; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("short special cost=%.8f want=%.8f", short, wantShort)
	}

	// long tier
	long, known := p.CostText("gemini-3.1-pro-preview", TextTokenUsage{
		InputTokens:         250000,
		OutputTokens:        500,
		ReasoningTokens:     100,
		CachedTokens:        200,
		CacheCreationTokens: 50,
		RequestInputTokens:  250000,
	})
	if !known {
		t.Fatal("expected known")
	}
	// non-cached = 250000-50-200 = 249750
	wantLong := 249750/1_000_000.0*4.00 + 200/1_000_000.0*0.40 + 50/1_000_000.0*5.00 + 400/1_000_000.0*18.00 + 100/1_000_000.0*2.00
	if diff := long - wantLong; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("long special cost=%.8f want=%.8f", long, wantLong)
	}
}

func TestNormalizeImageSize(t *testing.T) {
	cases := []struct {
		in        string
		want      string
		defaulted bool
	}{
		{"1024x1024", "1K", false},
		{"1024", "1K", false},
		{"1K", "1K", false},
		{"1k", "1K", false},
		{"2048x2048", "2K", false},
		{"2K", "2K", false},
		{"2k", "2K", false},
		{"default", "default", false},
		{"", "default", true},
		{"4096x4096", "default", true},
		{"unknown", "default", true},
	}
	for _, tc := range cases {
		got, def := NormalizeImageSize(tc.in)
		if got != tc.want || def != tc.defaulted {
			t.Fatalf("NormalizeImageSize(%q)=(%q,%v) want (%q,%v)", tc.in, got, def, tc.want, tc.defaulted)
		}
	}
}

func TestCostText_LongOmitsOptionalDoesNotInheritShort(t *testing.T) {
	p, err := Parse([]byte(`
pricing:
  tiered:
    input_per_1m: 1.00
    cached_input_per_1m: 0.10
    output_per_1m: 2.00
    reasoning_per_1m: 9.00
    cache_creation_per_1m: 8.00
    long_context:
      threshold_input_tokens: 1000
      input_per_1m: 4.00
      cached_input_per_1m: 0.40
      output_per_1m: 6.00
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cost, known := p.CostText("tiered", TextTokenUsage{
		InputTokens:         2000,
		OutputTokens:        500,
		ReasoningTokens:     100,
		CacheCreationTokens: 50,
		RequestInputTokens:  2000,
	})
	if !known {
		t.Fatal("expected known")
	}
	// non-cached=1950 long input; cache creation falls back to long input; all output at long output
	want := 1950/1_000_000.0*4.00 + 50/1_000_000.0*4.00 + 500/1_000_000.0*6.00
	if diff := cost - want; diff < -0.0001 || diff > 0.0001 {
		t.Fatalf("cost=%.8f want=%.8f", cost, want)
	}
	wrong := 1950/1_000_000.0*4.00 + 50/1_000_000.0*8.00 + 400/1_000_000.0*6.00 + 100/1_000_000.0*9.00
	if diff := cost - wrong; diff > -0.0001 && diff < 0.0001 {
		t.Fatal("cost matches short specialty inheritance")
	}
}

func TestParse_RejectsNullLongContext(t *testing.T) {
	_, err := Parse([]byte(`
pricing:
  m:
    input_per_1m: 1.0
    output_per_1m: 2.0
    long_context: null
`))
	if err == nil {
		t.Fatal("expected long_context null to be rejected")
	}
	if !strings.Contains(err.Error(), "pricing.m.long_context") {
		t.Fatalf("error path should include long_context: %v", err)
	}
}

func TestParse_RequiredFieldsTable(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantSub string
	}{
		{
			name: "base missing input",
			body: `
pricing:
  m:
    output_per_1m: 1.0
`,
			wantSub: "pricing.m.input_per_1m",
		},
		{
			name: "base missing output",
			body: `
pricing:
  m:
    input_per_1m: 1.0
`,
			wantSub: "pricing.m.output_per_1m",
		},
		{
			name: "long missing input",
			body: `
pricing:
  m:
    input_per_1m: 1.0
    output_per_1m: 2.0
    long_context:
      threshold_input_tokens: 10
      output_per_1m: 4.0
`,
			wantSub: "pricing.m.long_context.input_per_1m",
		},
		{
			name: "long missing output",
			body: `
pricing:
  m:
    input_per_1m: 1.0
    output_per_1m: 2.0
    long_context:
      threshold_input_tokens: 10
      input_per_1m: 4.0
`,
			wantSub: "pricing.m.long_context.output_per_1m",
		},
		{
			name: "long missing threshold",
			body: `
pricing:
  m:
    input_per_1m: 1.0
    output_per_1m: 2.0
    long_context:
      input_per_1m: 4.0
      output_per_1m: 6.0
`,
			wantSub: "pricing.m.long_context.threshold_input_tokens",
		},
		{
			name: "explicit zero base prices allowed",
			body: `
pricing:
  m:
    input_per_1m: 0
    output_per_1m: 0
`,
			wantSub: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body))
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestParse_RejectsNaNAndInf(t *testing.T) {
	cases := []struct {
		name string
		body string
		sub  string
	}{
		{
			name: "nan",
			body: `
pricing:
  m:
    input_per_1m: .nan
    output_per_1m: 1.0
`,
			sub: "pricing.m.input_per_1m",
		},
		{
			name: "inf",
			body: `
pricing:
  m:
    input_per_1m: 1.0
    output_per_1m: .inf
`,
			sub: "pricing.m.output_per_1m",
		},
		{
			name: "neg inf",
			body: `
pricing:
  m:
    input_per_1m: -.inf
    output_per_1m: 1.0
`,
			sub: "pricing.m.input_per_1m",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.sub) {
				t.Fatalf("error %q missing path %q", err.Error(), tc.sub)
			}
		})
	}
}

func TestParse_RejectsNullOrEmptyPerImageOutput(t *testing.T) {
	bodies := []string{
		"multimodal_pricing:\n  img:\n    image:\n      per_image_output: null\n",
		"multimodal_pricing:\n  img:\n    image:\n      per_image_output: {}\n",
	}
	for _, body := range bodies {
		_, err := Parse([]byte(body))
		if err == nil {
			t.Fatalf("expected error for body %q", body)
		}
		if !strings.Contains(err.Error(), "per_image_output") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestCostImages_Coverage(t *testing.T) {
	p, err := Parse([]byte(`
multimodal_pricing:
  imag:
    aliases: [imag-alias]
    image:
      per_image_input: 0.01
      per_image_output:
        default: 0.05
        "1K": 0.05
        "2K": 0.07
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cost, known, def := p.CostImages("imag", 3, 0, "1K")
	if !known || def || cost != 0.03 {
		t.Fatalf("input images cost=%v known=%v def=%v", cost, known, def)
	}

	cost, known, def = p.CostImages("imag-alias", 0, 2, "1024x1024")
	if !known || def || diffAbs(cost, 0.10) {
		t.Fatalf("1K output cost=%v known=%v def=%v", cost, known, def)
	}
	cost, known, def = p.CostImages("imag", 0, 2, "2K")
	if !known || def || diffAbs(cost, 0.14) {
		t.Fatalf("2K output cost=%v known=%v def=%v", cost, known, def)
	}
	cost, known, def = p.CostImages("imag", 0, 1, "")
	if !known || !def || diffAbs(cost, 0.05) {
		t.Fatalf("defaulted output cost=%v known=%v def=%v", cost, known, def)
	}

	cost, known, _ = p.CostImages("imag", 0, 0, "1K")
	if !known || cost != 0 {
		t.Fatalf("zero counts cost=%v known=%v", cost, known)
	}
	cost, known, _ = p.CostImages("totally-unknown", 0, 0, "1K")
	if !known || cost != 0 {
		t.Fatalf("zero counts unknown model cost=%v known=%v", cost, known)
	}
	cost, known, _ = p.CostImages("imag", -5, -3, "1K")
	if !known || cost != 0 {
		t.Fatalf("negative counts cost=%v known=%v", cost, known)
	}

	pFree, err := Parse([]byte(`
multimodal_pricing:
  imag:
    image:
      per_image_input: 0
      per_image_output:
        default: 0.05
`))
	if err != nil {
		t.Fatalf("Parse free: %v", err)
	}
	cost, known, _ = pFree.CostImages("imag", 2, 0, "1K")
	if !known || cost != 0 {
		t.Fatalf("explicit free input cost=%v known=%v", cost, known)
	}

	pOutOnly, err := Parse([]byte(`
multimodal_pricing:
  imag:
    image:
      per_image_output:
        default: 0.05
        "1K": 0.05
`))
	if err != nil {
		t.Fatalf("Parse out-only: %v", err)
	}
	cost, known, _ = pOutOnly.CostImages("imag", 2, 1, "1K")
	if known {
		t.Fatal("missing per_image_input should make known=false when input count positive")
	}
	if diffAbs(cost, 0.05) {
		t.Fatalf("should keep known output subtotal, got %v", cost)
	}

	pInOnly, err := Parse([]byte(`
multimodal_pricing:
  imag:
    image:
      per_image_input: 0.01
`))
	if err != nil {
		t.Fatalf("Parse in-only: %v", err)
	}
	cost, known, _ = pInOnly.CostImages("imag", 2, 1, "1K")
	if known {
		t.Fatal("missing per_image_output should be unknown when output count positive")
	}
	if diffAbs(cost, 0.02) {
		t.Fatalf("should keep known input subtotal, got %v", cost)
	}
	cost, known, _ = pInOnly.CostImages("imag", 2, 0, "1K")
	if !known || cost != 0.02 {
		t.Fatalf("input-only cost=%v known=%v", cost, known)
	}

	cost, known, _ = p.CostImages("nope", 1, 0, "1K")
	if known || cost != 0 {
		t.Fatalf("unknown model cost=%v known=%v", cost, known)
	}
}

func TestHasTextPricingUsesNormalLookupRules(t *testing.T) {
	p, err := Parse([]byte(`
pricing:
  model-a:
    aliases: [model-alias]
    input_per_1m: 1
    output_per_1m: 2
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !p.HasTextPricing("model-a") || !p.HasTextPricing("model-alias") {
		t.Fatalf("configured model or alias not recognized")
	}
	if p.HasTextPricing("missing-model") {
		t.Fatal("missing model reported as text-priced")
	}
}

func TestHasMultimodal(t *testing.T) {
	p, err := Parse([]byte(`
multimodal_pricing:
  imag-canonical:
    aliases: [imag-alias]
    image:
      per_image_input: 0.01
      per_image_output:
        default: 0.05
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !p.HasMultimodal("imag-canonical") {
		t.Fatal("canonical should hit multimodal")
	}
	if !p.HasMultimodal("imag-alias") {
		t.Fatal("alias should hit multimodal")
	}
	if p.HasMultimodal("missing-model") {
		t.Fatal("unknown should not hit multimodal")
	}
	if !p.HasPerImagePricing("imag-alias") {
		t.Fatal("configured per-image alias should enable the per-image channel")
	}
	if p.HasImageTokenPricing("imag-alias") {
		t.Fatal("per-image-only model must not enable image token pricing")
	}
	p2, err := Parse([]byte(`
multimodal_pricing:
  imag-base:
    image:
      per_image_input: 0.01
      per_image_output:
        default: 0.05
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !p2.HasMultimodal("imag-base-2026-03-18") {
		t.Fatal("canonicalized date suffix should hit multimodal")
	}
	if !p2.HasPerImagePricing("imag-base-2026-03-18") {
		t.Fatal("canonicalized model should retain per-image configuration")
	}

	tokenOnly, err := Parse([]byte(`
multimodal_pricing:
  token-image:
    image:
      input_per_1m: 8
      output_per_1m: 30
`))
	if err != nil {
		t.Fatalf("Parse token-only: %v", err)
	}
	if !tokenOnly.HasMultimodal("token-image") || tokenOnly.HasPerImagePricing("token-image") {
		t.Fatal("token-only multimodal model must not enable per-image billing")
	}
	if !tokenOnly.HasImageTokenPricing("token-image") {
		t.Fatal("token-only multimodal model should expose image token pricing")
	}
}

func diffAbs(a, b float64) bool {
	d := a - b
	return d < -0.0001 || d > 0.0001
}

func TestNewPricing_StillUsableEmpty(t *testing.T) {
	p := NewPricing()
	if err := p.Validate(); err != nil {
		t.Fatalf("empty Validate: %v", err)
	}
	_, known := p.CostText("x", TextTokenUsage{InputTokens: 1, RequestInputTokens: 1})
	if known {
		t.Fatal("empty should not know models")
	}
	_, known, _ = p.CostImages("x", 1, 1, "1K")
	if known {
		t.Fatal("empty should not know multimodal models")
	}
	if p.HasMultimodal("x") {
		t.Fatal("empty HasMultimodal should be false")
	}
}
