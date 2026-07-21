package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIssuesReportFiltersExactKeyAndExcludesGlobalSources(t *testing.T) {
	d := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	keyA := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	keyB := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	if err := d.InsertBatch([]UsageRecord{
		{
			CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), APIKeyHash: keyA,
			Endpoint: "/v1/chat/completions", Method: "POST", Status: 429,
			ErrorClass: "rate_limited", ErrorCode: "a-rate", RequestID: "a-1",
			ModelRequested: "model-a", CaptureOutcome: "failed", CaptureReason: "response_error_event",
		},
		{
			CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), APIKeyHash: keyB,
			Endpoint: "/v1/responses", Method: "POST", Status: 401,
			ErrorClass: "auth_failed", RequestID: "b-1", ModelRequested: "model-b",
		},
		{
			CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
			Endpoint:  "/v1/messages", Method: "POST", Status: 500,
			ErrorClass: "upstream_5xx", RequestID: "u-1", ModelRequested: "model-c",
		},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	// Global side/credential/quota noise that must not appear under key scope.
	if _, err := d.InsertSideUsageEvent(SideUsageEvent{
		ReceivedAt: now.Add(-5 * time.Minute).Format(time.RFC3339), ReceivedAtUnix: now.Add(-5 * time.Minute).Unix(),
		RequestID: "side-1", MatchStatus: "conflict", Model: "side-model", Endpoint: "/v1/chat/completions",
		APIKeyHash: keyA, // different namespace; must never associate by this field
	}); err != nil {
		t.Fatalf("InsertSideUsageEvent: %v", err)
	}
	if err := d.UpsertCredentialHealth(&CredentialHealthRow{
		Provider: "codex", CredentialHash: "cred-1", Status: "error",
		CheckedAt: now.Format(time.RFC3339), CheckedAtUnix: now.Unix(), ErrorClass: "credential_error",
	}); err != nil {
		t.Fatalf("UpsertCredentialHealth: %v", err)
	}
	if err := d.UpsertQuotaCurrent(&QuotaCurrentRow{
		Provider: "codex", CredentialHash: "cred-1", WindowKey: "default",
		CheckedAt: now.Format(time.RFC3339), CheckedAtUnix: now.Unix(), Status: "low",
		QuotaSupported: 1, AdapterStatus: "available", LimitAmount: 100, RemainingAmount: 5, Unit: "requests",
	}); err != nil {
		t.Fatalf("UpsertQuotaCurrent: %v", err)
	}

	// Key A: only that key's request_usage issue.
	data, err := d.IssuesReport(context.Background(), IssueFilter{
		Scope: ReportScope{Since: now.Add(-time.Hour), KeyHash: keyA},
		Limit: 20, IncludeGlobal: false,
	})
	if err != nil {
		t.Fatalf("IssuesReport keyA: %v", err)
	}
	if data.RequestUsageErr != nil {
		t.Fatalf("request_usage err: %v", data.RequestUsageErr)
	}
	if len(data.RequestUsage) != 1 || data.RequestUsage[0].APIKeyHash != keyA || data.RequestUsage[0].Class != "rate_limited" {
		t.Fatalf("keyA request_usage = %+v", data.RequestUsage)
	}
	if len(data.SideChannel) != 0 || len(data.Credential) != 0 || len(data.Quota) != 0 {
		t.Fatalf("key-scoped global sources not empty: side=%+v cred=%+v quota=%+v", data.SideChannel, data.Credential, data.Quota)
	}

	// Key B isolation.
	dataB, err := d.IssuesReport(context.Background(), IssueFilter{
		Scope: ReportScope{Since: now.Add(-time.Hour), KeyHash: keyB},
		Limit: 20, IncludeGlobal: false,
	})
	if err != nil || dataB.RequestUsageErr != nil {
		t.Fatalf("IssuesReport keyB: %v / %v", err, dataB.RequestUsageErr)
	}
	if len(dataB.RequestUsage) != 1 || dataB.RequestUsage[0].APIKeyHash != keyB || dataB.RequestUsage[0].Class != "auth_failed" {
		t.Fatalf("keyB request_usage = %+v", dataB.RequestUsage)
	}

	// Unknown maps blank/null key hashes.
	dataU, err := d.IssuesReport(context.Background(), IssueFilter{
		Scope: ReportScope{Since: now.Add(-time.Hour), KeyHash: "unknown"},
		Limit: 20, IncludeGlobal: false,
	})
	if err != nil || dataU.RequestUsageErr != nil {
		t.Fatalf("IssuesReport unknown: %v / %v", err, dataU.RequestUsageErr)
	}
	if len(dataU.RequestUsage) != 1 || dataU.RequestUsage[0].RequestID != "u-1" || dataU.RequestUsage[0].APIKeyHash != "" {
		t.Fatalf("unknown request_usage = %+v", dataU.RequestUsage)
	}

	// Global includes optional sources.
	global, err := d.IssuesReport(context.Background(), IssueFilter{
		Scope: ReportScope{Since: now.Add(-time.Hour)},
		Limit: 50, IncludeGlobal: true,
	})
	if err != nil || global.RequestUsageErr != nil {
		t.Fatalf("IssuesReport global: %v / %v", err, global.RequestUsageErr)
	}
	if global.SideChannelErr != nil || global.CredentialErr != nil || global.QuotaErr != nil {
		t.Fatalf("optional errs: side=%v cred=%v quota=%v", global.SideChannelErr, global.CredentialErr, global.QuotaErr)
	}
	if len(global.RequestUsage) < 3 {
		t.Fatalf("global request_usage = %+v, want >=3", global.RequestUsage)
	}
	if len(global.SideChannel) == 0 || len(global.Credential) == 0 || len(global.Quota) == 0 {
		t.Fatalf("global optional empty: side=%+v cred=%+v quota=%+v", global.SideChannel, global.Credential, global.Quota)
	}
}

func TestIssuesReportHonorsCanceledContext(t *testing.T) {
	d := newTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data, err := d.IssuesReport(ctx, IssueFilter{
		Scope: ReportScope{Since: time.Now().Add(-time.Hour)},
		Limit: 10, IncludeGlobal: true,
	})
	if data != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("IssuesReport data=%v err=%v, want nil data and canceled", data, err)
	}
}
