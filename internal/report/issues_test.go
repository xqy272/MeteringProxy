package report

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

func TestIssuesKeyScopedExcludesGlobalAndSystem(t *testing.T) {
	keyHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	reader := &stubModelsReader{
		issuesData: &db.IssuesReportData{
			RequestUsage: []db.IssueRow{{
				Class: "rate_limited", Label: "Rate limited", Count: 2, Severity: "warning",
				SourceGroup: "request_usage", APIKeyHash: keyHash, RequestID: "req-a",
			}},
			// Poison optional sources; key-scoped path must not query/include them.
			SideChannel: []db.IssueRow{{Class: "usage_conflict", SourceGroup: "side_channel"}},
			Credential:  []db.IssueRow{{Class: "credential_error", SourceGroup: "credential_health"}},
			Quota:       []db.IssueRow{{Class: "quota_low", SourceGroup: "quota"}},
		},
		errorTimeline: []db.ErrorTimelineRow{{ParseErrors: 9, DBErrors: 3, DroppedEvents: 1}},
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.Issues(context.Background(), IssueFilter{
		Since: time.Now().Add(-time.Hour), KeyHash: keyHash, Limit: 20, Range: "24h",
	})
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	if reader.issueFilter.Scope.KeyHash != keyHash || reader.issueFilter.IncludeGlobal {
		t.Fatalf("filter key = %q includeGlobal=%v", reader.issueFilter.Scope.KeyHash, reader.issueFilter.IncludeGlobal)
	}
	if out.Total != 1 || len(out.Items) != 1 || out.Items[0].Class != "rate_limited" || out.Items[0].APIKeyHash != keyHash {
		t.Fatalf("items = %+v", out)
	}
	if len(out.System.Items) != 0 || out.System.ParseErrors != 0 || out.System.DBErrors != 0 {
		t.Fatalf("key-scoped system = %+v, want empty", out.System)
	}
	if out.Partial {
		t.Fatalf("key-scoped partial = true, want false")
	}
	if out.Sources.RequestUsage != IssueSourceComplete ||
		out.Sources.SideChannel != IssueSourceNotApplicable ||
		out.Sources.CredentialHealth != IssueSourceNotApplicable ||
		out.Sources.Quota != IssueSourceNotApplicable ||
		out.Sources.System != IssueSourceNotApplicable {
		t.Fatalf("sources = %+v", out.Sources)
	}
}

func TestIssuesGlobalPartialSourceStatuses(t *testing.T) {
	reader := &stubModelsReader{
		issuesData: &db.IssuesReportData{
			RequestUsage:   []db.IssueRow{{Class: "auth_failed", Label: "Auth failed", Count: 1, Severity: "error", SourceGroup: "request_usage"}},
			SideChannelErr: errors.New("side channel query failed"),
			Credential:     []db.IssueRow{{Class: "credential_error", Label: "Credential health error", Count: 1, Severity: "error", SourceGroup: "credential_health"}},
			Quota:          []db.IssueRow{{Class: "quota_low", Label: "Quota low", Count: 1, Severity: "warning", SourceGroup: "quota"}},
			QuotaErr:       errors.New("quota refresh query failed"),
		},
		errorTimelineErr:   errors.New("timeline failed"),
		runtimeDropped:     4,
		runtimeParseErrors: 2,
		runtimeDBErrors:    1,
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.Issues(context.Background(), IssueFilter{
		Since: time.Now().Add(-time.Hour), Limit: 20, Range: "24h",
	})
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	if !out.Partial {
		t.Fatalf("partial = false, want true")
	}
	if out.Sources.RequestUsage != IssueSourceComplete ||
		out.Sources.SideChannel != IssueSourceUnavailable ||
		out.Sources.CredentialHealth != IssueSourceComplete ||
		out.Sources.Quota != IssueSourceUnavailable ||
		out.Sources.System != IssueSourceUnavailable {
		t.Fatalf("sources = %+v", out.Sources)
	}
	// Successful sources retained; side_channel omitted; quota rows retained under partial.
	classes := map[string]bool{}
	for _, item := range out.Items {
		classes[item.Class] = true
	}
	if !classes["auth_failed"] || !classes["credential_error"] || !classes["quota_low"] || classes["usage_conflict"] {
		t.Fatalf("items classes = %v", classes)
	}
	if out.System.ParseErrors != 2 || out.System.DBErrors != 1 || out.System.DroppedEvents != 4 {
		t.Fatalf("system counts = %+v, want process fallback counters", out.System)
	}
}

func TestIssuesRequestUsageFailureIsAtomic(t *testing.T) {
	want := errors.New("request_usage failed")
	reader := &stubModelsReader{issuesErr: want}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	if _, err := svc.Issues(context.Background(), IssueFilter{Range: "24h"}); !errors.Is(err, want) {
		t.Fatalf("err=%v, want %v", err, want)
	}
}

func TestIssuesContextCancellationPropagatesFromOptionalSources(t *testing.T) {
	reader := &stubModelsReader{
		issuesData: &db.IssuesReportData{
			RequestUsage:   []db.IssueRow{{Class: "auth_failed", SourceGroup: "request_usage"}},
			SideChannelErr: context.Canceled,
			// Poison later sources: cancellation must fail before treating these as partial.
			Credential: []db.IssueRow{{Class: "credential_error", SourceGroup: "credential_health"}},
			Quota:      []db.IssueRow{{Class: "quota_low", SourceGroup: "quota"}},
		},
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.Issues(context.Background(), IssueFilter{Range: "24h"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want canceled", err)
	}
	if out.Total != 0 || out.Partial || len(out.Items) != 0 {
		t.Fatalf("canceled response must not partially assemble: %+v", out)
	}
}

func TestIssuesContextCancellationPropagatesFromErrorTimeline(t *testing.T) {
	reader := &stubModelsReader{
		issuesData: &db.IssuesReportData{
			RequestUsage: []db.IssueRow{{Class: "auth_failed", SourceGroup: "request_usage"}},
		},
		errorTimelineErr:   context.Canceled,
		runtimeDropped:     9,
		runtimeParseErrors: 8,
		runtimeDBErrors:    7,
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.Issues(context.Background(), IssueFilter{Range: "24h"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want canceled", err)
	}
	// Must not fall back to process counters on cancellation.
	if out.System.DroppedEvents != 0 || out.System.ParseErrors != 0 || out.System.DBErrors != 0 || out.Partial {
		t.Fatalf("canceled system fallback leaked: %+v", out)
	}
}

func TestIssuesJSONCompatibilityEmptySlices(t *testing.T) {
	reader := &stubModelsReader{
		issuesData: &db.IssuesReportData{},
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.Issues(context.Background(), IssueFilter{Range: "24h"})
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal map: %v", err)
	}
	for _, key := range []string{"items", "partial", "sources", "system", "range", "total"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing field %q in %s", key, raw)
		}
	}
	if string(decoded["items"]) != "[]" {
		t.Fatalf("items = %s, want []", decoded["items"])
	}
	var system map[string]json.RawMessage
	if err := json.Unmarshal(decoded["system"], &system); err != nil {
		t.Fatalf("system: %v", err)
	}
	if string(system["items"]) != "[]" {
		t.Fatalf("system.items = %s, want []", system["items"])
	}
}

func TestIssuesEmptyTimelineFallsBackToProcessWithoutDegradation(t *testing.T) {
	reader := &stubModelsReader{
		issuesData:         &db.IssuesReportData{},
		errorTimeline:      nil, // empty, not error
		runtimeDropped:     7,
		runtimeParseErrors: 3,
		runtimeDBErrors:    2,
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.Issues(context.Background(), IssueFilter{Range: "24h"})
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	if out.Partial || out.Sources.System != IssueSourceComplete {
		t.Fatalf("partial=%v system=%s, want complete process fallback", out.Partial, out.Sources.System)
	}
	if out.System.DroppedEvents != 7 || out.System.ParseErrors != 3 || out.System.DBErrors != 2 {
		t.Fatalf("system = %+v", out.System)
	}
	if len(out.System.Items) != 3 {
		t.Fatalf("system items = %+v", out.System.Items)
	}
	for _, item := range out.System.Items {
		if item.Scope != "process" {
			t.Fatalf("item scope = %q, want process", item.Scope)
		}
	}
}
