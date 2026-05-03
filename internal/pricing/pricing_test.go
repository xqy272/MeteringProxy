package pricing

import (
	"os"
	"path/filepath"
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
		{"gpt-4o-abc", "gpt-4o-abc"},     // not a date suffix
		{"gpt-4o-20", "gpt-4o-20"},       // too short for date suffix
	}
	for _, tc := range tests {
		got := canonicalize(tc.input)
		if got != tc.want {
			t.Errorf("canonicalize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
