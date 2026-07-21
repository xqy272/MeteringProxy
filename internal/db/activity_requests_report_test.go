package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestActivityAndRequestsReportsFilterExactKey(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	keyA := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	keyB := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := d.InsertBatch([]UsageRecord{
		{
			CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), APIKeyHash: keyA,
			RequestID: "a-ok", Endpoint: "/v1/chat/completions", EndpointProfile: "chat", Method: "POST", Status: 200,
			LatencyMs: 100, TTFBMs: 20, ModelReturned: "model-a", CaptureOutcome: "captured",
		},
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), APIKeyHash: keyA,
			RequestID: "a-error", Endpoint: "/v1/responses", EndpointProfile: "responses", Method: "POST", Status: 500,
			LatencyMs: 300, TTFBMs: 80, ModelReturned: "model-b", ErrorClass: "upstream_5xx", ErrorCode: "server_error", CaptureOutcome: "failed",
		},
		{
			CreatedAt: now.Add(-5 * time.Minute).Format(time.RFC3339), APIKeyHash: keyB,
			RequestID: "b-error", Endpoint: "/v1/responses", EndpointProfile: "responses", Method: "POST", Status: 429,
			LatencyMs: 900, TTFBMs: 200, ModelReturned: "model-b", ErrorClass: "rate_limited", CaptureOutcome: "failed",
		},
		{
			CreatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339),
			RequestID: "unknown-ok", Endpoint: "/v1/messages", EndpointProfile: "messages", Method: "POST", Status: 200,
			LatencyMs: 50, TTFBMs: 10, ModelReturned: "model-c", CaptureOutcome: "captured",
		},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	scopeA := ReportScope{Since: now.Add(-time.Hour), KeyHash: keyA}
	activity, err := d.ActivityReport(context.Background(), scopeA)
	if err != nil {
		t.Fatalf("ActivityReport: %v", err)
	}
	if activity.SampleSize != 2 || activity.SuccessCount != 1 || activity.FailedCount != 1 || activity.P95LatencyMs != 300 || activity.P95TTFBMs != 80 || activity.LatestErrorStatus != 500 || activity.LatestErrorModel != "model-b" {
		t.Fatalf("activity = %+v", activity)
	}

	requests, err := d.RequestsReport(context.Background(), RequestFilter{
		Scope: scopeA, Limit: 10, StatusMin: 500,
		Model: "model-b", Endpoint: "profile:responses", ErrorClass: "upstream_5xx",
	})
	if err != nil {
		t.Fatalf("RequestsReport: %v", err)
	}
	if len(requests) != 1 || requests[0].RequestID != "a-error" || requests[0].APIKeyHash != keyA {
		t.Fatalf("requests = %+v", requests)
	}
	unknown, err := d.RequestsReport(context.Background(), RequestFilter{
		Scope: ReportScope{Since: now.Add(-time.Hour), KeyHash: "unknown"}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("RequestsReport unknown: %v", err)
	}
	if len(unknown) != 1 || unknown[0].RequestID != "unknown-ok" {
		t.Fatalf("unknown requests = %+v", unknown)
	}
}

func TestActivityAndRequestsReportsHonorCanceledContext(t *testing.T) {
	d := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scope := ReportScope{Since: time.Now().Add(-time.Hour)}
	if _, err := d.ActivityReport(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("ActivityReport err=%v, want canceled", err)
	}
	if _, err := d.RequestsReport(ctx, RequestFilter{Scope: scope}); !errors.Is(err, context.Canceled) {
		t.Fatalf("RequestsReport err=%v, want canceled", err)
	}
}
