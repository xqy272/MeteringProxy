package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

func TestActivityAndRequestsForwardTypedFiltersAndMapCompatibility(t *testing.T) {
	keyHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	reader := &stubModelsReader{
		activityRow: &db.ActivityRow{SampleSize: 2, FailedCount: 1, FailureRate: 0.5, P95LatencyMs: 300},
		requestRows: []db.RequestRow{{
			ID: 7, RequestID: "req-7", APIKeyHash: keyHash,
			ModelReturned: "model-a", ModelReturnedSource: "response_body",
			CaptureMode: "request_only", CaptureOutcome: "skipped",
		}},
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	since := time.Now().Add(-time.Hour)
	activity, err := svc.Activity(context.Background(), ActivityFilter{Since: since, KeyHash: keyHash})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if activity.SampleSize != 2 || activity.FailureRate != 0.5 || reader.lastKeyHash != keyHash {
		t.Fatalf("activity=%+v key=%q", activity, reader.lastKeyHash)
	}
	requests, err := svc.Requests(context.Background(), RequestFilter{
		Since: since, KeyHash: keyHash, Limit: 20, StatusMin: 400, StatusMax: 500,
		Model: "model-a", Endpoint: "profile:responses", ErrorClass: "rate_limited",
	})
	if err != nil {
		t.Fatalf("Requests: %v", err)
	}
	if len(requests) != 1 || requests[0].RequestID != "req-7" || requests[0].UsageConfidence != "request_only" {
		t.Fatalf("requests = %+v", requests)
	}
	got := reader.requestFilter
	if got.Scope.KeyHash != keyHash || !got.Scope.Since.Equal(since) || got.Limit != 20 || got.StatusMin != 400 || got.StatusMax != 500 || got.Model != "model-a" || got.Endpoint != "profile:responses" || got.ErrorClass != "rate_limited" {
		t.Fatalf("request filter = %+v", got)
	}
}

func TestActivityAndRequestsPropagateErrorsAndEmptySlices(t *testing.T) {
	want := errors.New("report failed")
	reader := &stubModelsReader{activityErr: want, requestErr: want}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	if _, err := svc.Activity(context.Background(), ActivityFilter{}); !errors.Is(err, want) {
		t.Fatalf("Activity err=%v", err)
	}
	if rows, err := svc.Requests(context.Background(), RequestFilter{}); !errors.Is(err, want) || rows != nil {
		t.Fatalf("Requests rows=%#v err=%v", rows, err)
	}
	reader.requestErr = nil
	rows, err := svc.Requests(context.Background(), RequestFilter{})
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty Requests rows=%#v err=%v", rows, err)
	}
}
