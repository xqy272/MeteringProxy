package report

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/profile"
)

func TestMultimodalSummaryEmptySliceAndJSON(t *testing.T) {
	reader := &stubModelsReader{}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.MultimodalSummary(context.Background(), MultimodalFilter{Since: time.Now()})
	if err != nil {
		t.Fatalf("MultimodalSummary: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil empty slice")
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Fatalf("json = %s, want []", b)
	}
}

func TestImageRequestsUsesRequestReportConfidence(t *testing.T) {
	reader := &stubModelsReader{
		imageRequestRows: []db.RequestRow{{
			ID: 1, CaptureOutcome: "captured", UsageSource: "http_response",
		}},
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.ImageRequests(context.Background(), ImageRequestsFilter{Limit: 10, Since: time.Now()})
	if err != nil {
		t.Fatalf("ImageRequests: %v", err)
	}
	if len(out) != 1 || out[0].UsageConfidence != "observed" {
		t.Fatalf("out=%+v", out)
	}
}

func TestErrorsOptionalSourcePartialAndCancellation(t *testing.T) {
	reader := &stubModelsReader{
		errorTimelineErr:     errors.New("health db down"),
		errorTimelineFromReq: []db.ErrorTimelineRow{{Timestamp: "2026-01-01T00:00:00Z", Count: 2}},
		latestHealth:         &db.HealthRow{QueueDepth: 3, ParseErrors: 4},
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.Errors(context.Background(), ErrorsFilter{Since: time.Now()})
	if err != nil {
		t.Fatalf("Errors: %v", err)
	}
	if !out.Partial || out.SourceStatuses.HealthMetrics != SourceUnavailable {
		t.Fatalf("partial/source = %+v", out)
	}
	if out.Source != "request_usage" || len(out.Timeline) != 1 || out.Timeline[0].Count != 2 {
		t.Fatalf("timeline response = %+v", out)
	}
	if out.QueueDepth != 3 || out.ParseErrors != 4 {
		t.Fatalf("latest health fields = %+v", out)
	}

	// Both sources failed => top-level error.
	reader.errorTimelineFromReqErr = errors.New("request db down")
	if _, err := svc.Errors(context.Background(), ErrorsFilter{Since: time.Now()}); err == nil {
		t.Fatal("expected top-level error when both sources fail")
	}

	// Cancellation is always top-level.
	reader.errorTimelineErr = context.Canceled
	reader.errorTimelineFromReqErr = nil
	if _, err := svc.Errors(context.Background(), ErrorsFilter{Since: time.Now()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want canceled", err)
	}
}

func TestHealthEmptyLatestIsNotComplete(t *testing.T) {
	reader := &stubModelsReader{latestHealth: nil}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	out, err := svc.Health(context.Background(), HealthFilter{MeteringEnabled: true})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if out.SourceStatuses.LatestHealth != SourceEmpty {
		t.Fatalf("latest status=%q, want empty", out.SourceStatuses.LatestHealth)
	}
	// Preserve zero-object JSON shape for latest_health.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"latest_health":{`) {
		t.Fatalf("latest_health missing zero object: %s", b)
	}
	if out.MeteringEnabled != true || out.CaptureDisabled != false {
		t.Fatalf("metering flags = %+v", out)
	}

	reader.latestHealthErr = context.DeadlineExceeded
	if _, err := svc.Health(context.Background(), HealthFilter{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want deadline", err)
	}
}

func TestGatewayCapabilitiesDBFailureAndMetadataProfiles(t *testing.T) {
	reader := &stubModelsReader{gatewayErr: errors.New("sql: no such table secrets")}
	deps := testDependencies(reader)
	deps.Profiles = profile.NewRegistry()
	svc := NewService(deps, &stubCostEngine{})
	if _, err := svc.GatewayCapabilities(context.Background(), GatewayFilter{Range: "24h", Since: time.Now()}); err == nil {
		t.Fatal("expected gateway DB failure")
	}

	meta, err := svc.Metadata(context.Background(), MetadataFilter{})
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if len(meta.Endpoints) == 0 {
		t.Fatal("expected registry endpoints in metadata")
	}
	b, err := json.Marshal(meta.Endpoints)
	if err != nil {
		t.Fatalf("marshal endpoints: %v", err)
	}
	if string(b) == "null" {
		t.Fatal("endpoints must not be null")
	}
}

func TestGatewayCapabilitiesMergesUnknownAndSortsExtras(t *testing.T) {
	reader := &stubModelsReader{gatewayRows: []db.GatewayCapabilityRow{
		{EndpointProfile: "legacy-b", RequestCount: 1, PassthroughCount: 1},
		{EndpointProfile: "legacy-a", RequestCount: 2, PassthroughCount: 2},
		{EndpointProfile: "unknown", RequestCount: 3, PassthroughCount: 3},
		{EndpointProfile: "chat_completions", RequestCount: 4, UsageMeteredCount: 4},
	}}
	deps := testDependencies(reader)
	deps.Profiles = profile.NewRegistry()
	svc := NewService(deps, &stubCostEngine{})
	out, err := svc.GatewayCapabilities(context.Background(), GatewayFilter{Range: "24h", Since: time.Now()})
	if err != nil {
		t.Fatalf("GatewayCapabilities: %v", err)
	}
	byName := map[string]GatewayCapabilityProfile{}
	for _, p := range out.Profiles {
		byName[p.Name] = p
	}
	if byName["unknown_passthrough"].RequestCount != 3 {
		t.Fatalf("unknown folded into passthrough = %+v", byName["unknown_passthrough"])
	}
	// DB-only names are sorted.
	var extras []string
	for _, p := range out.Profiles {
		if p.Name == "legacy-a" || p.Name == "legacy-b" {
			extras = append(extras, p.Name)
		}
	}
	if len(extras) != 2 || extras[0] != "legacy-a" || extras[1] != "legacy-b" {
		t.Fatalf("extras order = %v", extras)
	}
}

func TestGatewayCapabilitiesKeepsUnknownWithoutProfileSource(t *testing.T) {
	reader := &stubModelsReader{gatewayRows: []db.GatewayCapabilityRow{{
		EndpointProfile:  "unknown",
		RequestCount:     3,
		PassthroughCount: 3,
	}}}
	deps := testDependencies(reader)
	deps.Profiles = nil
	svc := NewService(deps, &stubCostEngine{})

	out, err := svc.GatewayCapabilities(context.Background(), GatewayFilter{Range: "24h", Since: time.Now()})
	if err != nil {
		t.Fatalf("GatewayCapabilities: %v", err)
	}
	if len(out.Profiles) != 1 || out.Profiles[0].Name != "unknown" || out.Profiles[0].RequestCount != 3 {
		t.Fatalf("profiles = %+v, want DB-only unknown", out.Profiles)
	}
	if out.Summary.TotalRequests != 3 || out.Summary.PassthroughReqs != 3 {
		t.Fatalf("summary = %+v", out.Summary)
	}
}
