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
	// reasoning_tokens are a subset of output_tokens.
	// When no reasoning_per_1m is configured, the entire output_tokens
	// (which already includes reasoning) is billed at output_per_1m.
	// This should NOT double-charge.
	p, err := Load(writePricing(t, `
pricing:
  deepseek-chat:
    input_per_1m: 0.27
    cached_input_per_1m: 0.07
    output_per_1m: 1.10
    # No reasoning_per_1m; reasoning is included in output_per_1m
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 1000 output tokens including 200 reasoning tokens
	cost, known := p.Cost("deepseek-chat", 500, 1000, 200, 0)
	if !known {
		t.Fatal("expected known cost")
	}

	// Cost: (500 * 0.27) / 1M + (1000 * 1.10) / 1M
	expected := 500/1_000_000.0*0.27 + 1000/1_000_000.0*1.10
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f (diff=%.6f)", cost, expected, diff)
	}
}

func TestCost_ReasoningWithSeparatePrice_NoDoubleBilling(t *testing.T) {
	// reasoning_tokens are a subset of output_tokens.
	// When reasoning_per_1m is configured:
	//   regular_output = output_tokens - reasoning_tokens
	//   regular_output billed at output_per_1m
	//   reasoning billed at reasoning_per_1m
	// This must NOT double-charge.
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

	// 200 input (50 cached), 1000 output (200 reasoning)
	cost, known := p.Cost("claude-sonnet-4-6", 200, 1000, 200, 50)
	if !known {
		t.Fatal("expected known cost")
	}

	// regular output: (1000 - 200) = 800 tokens at $15/M
	// reasoning: 200 tokens at $3/M
	// cached input: 50 tokens at $0.30/M
	// non-cached input: (200 - 50) = 150 tokens at $3/M
	expected := 150/1_000_000.0*3.00 + 50/1_000_000.0*0.30 + 800/1_000_000.0*15.00 + 200/1_000_000.0*3.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f (diff=%.6f)", cost, expected, diff)
	}

	// Verify the wrong formula (double-billing) would give a different result
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

	// 1000 input tokens, 600 cached (cached is subset of input)
	cost, known := p.Cost("gpt-4o", 1000, 50, 0, 600)
	if !known {
		t.Fatal("expected known cost")
	}

	// non-cached: 1000 - 600 = 400 at $2.50/M
	// cached: 600 at $1.25/M
	expected := 400/1_000_000.0*2.50 + 600/1_000_000.0*1.25 + 50/1_000_000.0*10.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f", cost, expected)
	}
}

func TestCost_ReasoningExceedsOutput(t *testing.T) {
	// Edge case: reasoning_tokens > output_tokens should not produce negative regular output
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

	// 300 reasoning but only 200 output; defensive, regular output clamped to 0
	cost, known := p.Cost("test", 100, 200, 300, 0)
	if !known {
		t.Fatal("expected known cost")
	}

	// regular_output = max(200-300, 0) = 0, so only 300 reasoning at $1/M
	expected := 100/1_000_000.0*1.00 + 0 + 300/1_000_000.0*1.00
	if diff := cost - expected; diff < -0.0001 || diff > 0.0001 {
		t.Errorf("cost = %.6f, want %.6f", cost, expected)
	}
}
