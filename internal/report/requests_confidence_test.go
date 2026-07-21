package report

import (
	"testing"

	"ai-gateway-metering-proxy/internal/db"
)

func TestRequestUsageConfidence(t *testing.T) {
	rows := []db.RequestRow{
		{UsageSource: "http_response", CaptureOutcome: "captured", InputTokens: 1},
		{UsageSource: "cliproxy_side_channel", CaptureOutcome: "captured", InputTokens: 1},
		{SideUsageMatchStatus: "conflict", InputTokens: 1},
		{CaptureMode: "usage_metered", CaptureOutcome: "skipped"},
		{CaptureMode: "request_only", CaptureOutcome: "captured"},
		{CaptureMode: "passthrough", CaptureOutcome: "captured"},
	}
	want := []string{"observed", "side_channel", "conflict", "missing_usage", "request_only", "unsupported"}
	for i := range want {
		got := requestUsageConfidence(rows[i])
		if got != want[i] {
			t.Fatalf("row %d UsageConfidence = %q, want %q", i, got, want[i])
		}
	}
}
