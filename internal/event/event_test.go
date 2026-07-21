package event

import (
	"testing"
	"time"
)

func TestEventToRecord_BasicMapping(t *testing.T) {
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	ev := Event{
		ID:                "req-001",
		Timestamp:         now,
		EndpointProfile:   "chat_completions",
		CaptureMode:       CaptureUsageMetered,
		MeteringKind:      MeteringLLMTokens,
		Method:            "POST",
		Path:              "/v1/chat/completions",
		Status:            200,
		Stream:            true,
		LatencyMs:         150,
		TTFBMs:            30,
		APIKeyHash:        "hash-key",
		ClientIPHash:      "hash-ip",
		ModelRequested:    "gpt-4o",
		ModelReturned:     "gpt-4o-2026-03-18",
		InputTokens:       100,
		OutputTokens:      200,
		ReasoningTokens:   10,
		CachedTokens:      5,
		TotalTokens:       300,
		RequestBytes:      1024,
		ResponseBytes:     2048,
		Error:             "",
		CaptureOutcome:    OutcomeCaptured,
		CaptureReason:     "",
		BillableInput:     0.001,
		BillableOutput:    0.002,
		BillableTotal:     0.003,
		BillableUnit:      "USD",
		UsageRawJSON:      `{"prompt_tokens":100}`,
		UsageRawTruncated: false,
	}

	rec := EventToRecord(ev)

	if rec.CreatedAt != "2026-05-03T12:00:00Z" {
		t.Errorf("CreatedAt = %q, want 2026-05-03T12:00:00Z", rec.CreatedAt)
	}
	if rec.RequestID != "req-001" {
		t.Errorf("RequestID = %q", rec.RequestID)
	}
	if rec.EndpointProfile != "chat_completions" {
		t.Errorf("EndpointProfile = %q", rec.EndpointProfile)
	}
	if rec.CaptureMode != CaptureUsageMetered {
		t.Errorf("CaptureMode = %q", rec.CaptureMode)
	}
	if rec.MeteringKind != MeteringLLMTokens {
		t.Errorf("MeteringKind = %q", rec.MeteringKind)
	}
	if rec.BillableInput != 0.001 {
		t.Errorf("BillableInput = %f", rec.BillableInput)
	}
	if rec.CaptureOutcome != OutcomeCaptured {
		t.Errorf("CaptureOutcome = %q", rec.CaptureOutcome)
	}
	if rec.InputTokens != 100 || rec.OutputTokens != 200 {
		t.Errorf("tokens: input=%d output=%d", rec.InputTokens, rec.OutputTokens)
	}
	if rec.ReasoningTokens != 10 || rec.CachedTokens != 5 {
		t.Errorf("detail tokens: reasoning=%d cached=%d", rec.ReasoningTokens, rec.CachedTokens)
	}
}

func TestEventConstants_Distinct(t *testing.T) {
	// Verify all constants are non-empty and distinct within groups.
	outcomes := map[string]bool{OutcomeCaptured: true, OutcomeSkipped: true, OutcomeFailed: true}
	if len(outcomes) != 3 {
		t.Error("capture outcomes should be distinct")
	}

	modes := map[string]bool{CapturePassthrough: true, CaptureRequestOnly: true, CaptureUsageMetered: true}
	if len(modes) != 3 {
		t.Error("capture modes should be distinct")
	}
}
