package compat

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/cliproxy"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/profile"
	"ai-gateway-metering-proxy/internal/proxy"
	"ai-gateway-metering-proxy/internal/writer"
)

func TestCLIProxyAPIv704ManagementContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[{"id":"codex-1","auth_index":"12","name":"codex@example.com","type":"codex","provider":"codex","label":"Codex Primary","status":"active","disabled":false,"unavailable":false,"success":7,"failed":2,"recent_requests":[]} ]}`))
		case "/v0/management/usage-queue":
			if r.URL.Query().Get("count") != "50" {
				t.Fatalf("usage-queue count = %q, want 50", r.URL.Query().Get("count"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"request_id":"req-cpa","provider":"codex","model":"gpt-5.4-mini","endpoint":"POST /v1/responses","tokens":{"input_tokens":10,"output_tokens":3,"cache_read_tokens":4,"cache_creation_tokens":2,"total_tokens":13}}]`))
		case "/v0/management/api-call":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"missing method"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := cliproxy.NewClient(cliproxy.CLIProxyConfig{
		BaseURL: server.URL + "/v0/management",
		Key:     "management-key",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	authFiles, err := client.FetchAuthFiles()
	if err != nil {
		t.Fatalf("FetchAuthFiles: %v", err)
	}
	if len(authFiles.AuthFiles) != 1 || authFiles.AuthFiles[0].Provider != "codex" || authFiles.AuthFiles[0].AuthIndex != "12" {
		t.Fatalf("auth-files contract decoded incorrectly: %#v", authFiles.AuthFiles)
	}
	records, err := client.FetchUsageQueue(50)
	if err != nil {
		t.Fatalf("FetchUsageQueue: %v", err)
	}
	if len(records) != 1 || !bytes.Contains(records[0], []byte(`"cache_creation_tokens":2`)) {
		t.Fatalf("usage queue contract decoded incorrectly: %s", records)
	}
	_, status, err := client.DoAPICall(http.MethodPost, "/api-call", bytes.NewReader([]byte(`{"provider":"probe"}`)))
	if err != nil {
		t.Fatalf("DoAPICall: %v", err)
	}
	if status != http.StatusBadRequest {
		t.Fatalf("api-call probe status = %d, want 400", status)
	}
}

func TestCLIProxyAPIv704RouteMatrix(t *testing.T) {
	registry := profile.NewRegistry()
	tests := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodPost, "/v1/chat/completions", "chat_completions"},
		{http.MethodPost, "/v1/completions", "openai_completions"},
		{http.MethodPost, "/v1/responses", "responses"},
		{http.MethodPost, "/v1/responses/compact", "responses"},
		{http.MethodPost, "/backend-api/codex/responses", "responses"},
		{http.MethodPost, "/backend-api/codex/responses/compact", "responses"},
		{http.MethodPost, "/v1/messages", "anthropic_messages"},
		{http.MethodPost, "/v1beta/models/gemini-2.5-pro:generateContent", "gemini_generate_content"},
		{http.MethodPost, "/api/provider/openai/v1/chat/completions", "chat_completions"},
		{http.MethodPost, "/api/provider/openai/v1/completions", "openai_completions"},
		{http.MethodPost, "/api/provider/openai/v1/responses", "responses"},
		{http.MethodPost, "/api/provider/anthropic/v1/messages", "anthropic_messages"},
		{http.MethodPost, "/api/provider/google/v1beta/models/gemini-2.5-pro:streamGenerateContent", "gemini_generate_content"},
		{http.MethodGet, "/v1/responses", "unknown_passthrough"},
		{http.MethodPost, "/v1/messages/count_tokens", "unknown_passthrough"},
		{http.MethodPost, "/v1/images/generations", "unknown_passthrough"},
		{http.MethodPost, "/v1/videos", "unknown_passthrough"},
		{http.MethodPost, "/v1/videos/generations", "unknown_passthrough"},
		{http.MethodGet, "/v1/videos/vid_123", "unknown_passthrough"},
	}
	for _, tc := range tests {
		prof, err := registry.Match(tc.method, tc.path)
		if err != nil {
			t.Fatalf("Match(%s %s): %v", tc.method, tc.path, err)
		}
		if prof.Name != tc.want {
			t.Fatalf("Match(%s %s) = %s, want %s", tc.method, tc.path, prof.Name, tc.want)
		}
	}
}

func TestCLIProxyAPIv704ProxyMetersCodexDirectResponses(t *testing.T) {
	upstreamPath := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-5.4-mini","usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}}`))
	}))
	defer upstream.Close()

	capture := &captureWriter{}
	p := proxy.New(upstream.URL, hash.NewWithSalt("test-salt"), capture, 1024*1024, config.RequestMetadataConfig{
		InitialBytes:      4096,
		MaxBytes:          65536,
		ExtendedModelScan: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", bytes.NewReader([]byte(`{"model":"gpt-5.4-mini"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if upstreamPath != "/backend-api/codex/responses" {
		t.Fatalf("upstream path = %q, want codex direct path preserved", upstreamPath)
	}
	events := capture.Events()
	if len(events) != 1 {
		t.Fatalf("captured events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.EndpointProfile != "responses" || ev.InputTokens != 10 || ev.OutputTokens != 3 || ev.TotalTokens != 13 {
		t.Fatalf("captured event = %#v", ev)
	}
}

type captureWriter struct {
	mu     sync.Mutex
	events []event.Event
}

func (w *captureWriter) Enqueue(ev writer.StatsEvent) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, ev.Event)
	return true
}

func (w *captureWriter) IncrParseErrors() {}

func (w *captureWriter) Events() []event.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]event.Event, len(w.events))
	copy(out, w.events)
	return out
}
