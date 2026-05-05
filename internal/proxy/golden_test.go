package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/writer"
)

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "testdata")); err == nil {
			return filepath.Join(dir, "testdata", "fixtures", name)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find testdata directory from repo root")
		}
		dir = parent
	}
}

type goldenTestRW struct {
	events      []writer.StatsEvent
	parseErrors int64
}

func (g *goldenTestRW) Enqueue(ev writer.StatsEvent) bool {
	g.events = append(g.events, ev)
	return true
}

func (g *goldenTestRW) IncrParseErrors() {
	g.parseErrors++
}

func goldenLastEvent(g *goldenTestRW) event.Event {
	if len(g.events) == 0 {
		return event.Event{}
	}
	return g.events[len(g.events)-1].Event
}

func TestGolden_ChatCompletionsStream_ByteTransparent(t *testing.T) {
	fixture := fixturePath(t, "chat_completions_stream.txt")
	expected, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("OpenAI-Request-ID", "req_golden_stream")
		w.Write(expected)
	}))
	defer upstream.Close()

	rw := &goldenTestRW{}
	p := New(upstream.URL, hash.NewWithSalt("golden-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"test"}]}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	got := rec.Body.Bytes()
	if !bytes.Equal(got, expected) {
		t.Errorf("SSE golden: byte mismatch (%d bytes got vs %d expected)", len(got), len(expected))
		for i := 0; i < len(got) && i < len(expected); i++ {
			if got[i] != expected[i] {
				t.Errorf("first diff at byte %d: got 0x%02x, want 0x%02x", i, got[i], expected[i])
				break
			}
		}
	}

	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded; golden SSE parse failed")
	}
	ev := goldenLastEvent(rw)
	if ev.InputTokens != 15 || ev.OutputTokens != 8 || ev.TotalTokens != 23 {
		t.Errorf("golden tokens: input=%d output=%d total=%d, want 15/8/23",
			ev.InputTokens, ev.OutputTokens, ev.TotalTokens)
	}
	if ev.ModelReturned != "gpt-4o-2026-03-18" {
		t.Errorf("golden model_returned = %q, want gpt-4o-2026-03-18", ev.ModelReturned)
	}
	if ev.ID != "req_golden_stream" {
		t.Errorf("golden request_id = %q, want req_golden_stream", ev.ID)
	}
}

func TestGolden_ChatCompletionsNonStream_ByteTransparent(t *testing.T) {
	fixture := fixturePath(t, "chat_completions_nonstream.json")
	expected, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("OpenAI-Request-ID", "req_golden_nonstream")
		w.Write(expected)
	}))
	defer upstream.Close()

	rw := &goldenTestRW{}
	p := New(upstream.URL, hash.NewWithSalt("golden-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"test"}]}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	got := rec.Body.Bytes()
	if !bytes.Equal(got, expected) {
		t.Errorf("non-stream golden: byte mismatch (%d bytes got vs %d expected)", len(got), len(expected))
	}

	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded; golden non-stream parse failed")
	}
	ev := goldenLastEvent(rw)
	if ev.InputTokens != 15 || ev.OutputTokens != 8 || ev.TotalTokens != 23 {
		t.Errorf("golden tokens: input=%d output=%d total=%d, want 15/8/23",
			ev.InputTokens, ev.OutputTokens, ev.TotalTokens)
	}
}

func TestGolden_ResponsesStream_ByteTransparent(t *testing.T) {
	fixture := fixturePath(t, "responses_stream.txt")
	expected, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write(expected)
	}))
	defer upstream.Close()

	rw := &goldenTestRW{}
	p := New(upstream.URL, hash.NewWithSalt("golden-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-5.4-mini","stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	got := rec.Body.Bytes()
	if !bytes.Equal(got, expected) {
		t.Errorf("responses SSE golden: byte mismatch (%d bytes got vs %d expected)", len(got), len(expected))
	}

	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded; golden Responses SSE parse failed")
	}
	ev := goldenLastEvent(rw)
	if ev.InputTokens != 20 || ev.OutputTokens != 10 || ev.TotalTokens != 30 {
		t.Errorf("golden tokens: input=%d output=%d total=%d, want 20/10/30",
			ev.InputTokens, ev.OutputTokens, ev.TotalTokens)
	}
	if ev.ReasoningTokens != 3 {
		t.Errorf("golden reasoning_tokens = %d, want 3", ev.ReasoningTokens)
	}
	if ev.CachedTokens != 5 {
		t.Errorf("golden cached_tokens = %d, want 5", ev.CachedTokens)
	}
}

func TestGolden_ResponsesNonStream_ByteTransparent(t *testing.T) {
	fixture := fixturePath(t, "responses_nonstream.json")
	expected, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(expected)
	}))
	defer upstream.Close()

	rw := &goldenTestRW{}
	p := New(upstream.URL, hash.NewWithSalt("golden-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"gpt-5.4-mini"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	got := rec.Body.Bytes()
	if !bytes.Equal(got, expected) {
		t.Errorf("responses non-stream golden: byte mismatch (%d bytes got vs %d expected)", len(got), len(expected))
	}

	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded; golden Responses non-stream parse failed")
	}
	ev := goldenLastEvent(rw)
	if ev.InputTokens != 20 || ev.OutputTokens != 10 || ev.TotalTokens != 30 {
		t.Errorf("golden tokens: input=%d output=%d total=%d, want 20/10/30",
			ev.InputTokens, ev.OutputTokens, ev.TotalTokens)
	}
}

func TestGolden_SSEAcrossChunks_UsageInFinalChunk(t *testing.T) {
	chunks := []string{
		"data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-2026-03-18\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"}}]}\n",
		"data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o-2026-03-18\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usa",
		"ge\":{\"prompt_tokens\":5,\"completion_tokens\":7,\"total_tokens\":12}}\n",
		"data: [DONE]\n",
	}
	var expected bytes.Buffer
	for _, c := range chunks {
		expected.WriteString(c)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, c := range chunks {
			w.Write([]byte(c))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	rw := &goldenTestRW{}
	p := New(upstream.URL, hash.NewWithSalt("golden-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"test"}]}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), expected.Bytes()) {
		t.Error("SSE cross-chunk golden: byte mismatch")
	}

	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded; cross-chunk golden parse failed")
	}
	ev := goldenLastEvent(rw)
	if ev.InputTokens != 5 || ev.OutputTokens != 7 || ev.TotalTokens != 12 {
		t.Errorf("cross-chunk golden tokens: input=%d output=%d total=%d, want 5/7/12",
			ev.InputTokens, ev.OutputTokens, ev.TotalTokens)
	}
}

func TestGolden_NonStreamReadWhileWrite(t *testing.T) {
	prefix := `{"model":"gpt-4o","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	padding := bytes.Repeat([]byte("x"), 2*1024*1024)
	fullResponse := append([]byte(prefix), padding...)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fullResponse)
	}))
	defer upstream.Close()

	rw := &goldenTestRW{}
	p := New(upstream.URL, hash.NewWithSalt("golden-salt"), rw, 1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.Len() != len(fullResponse) {
		t.Errorf("response truncated: got %d bytes, want %d", rec.Body.Len(), len(fullResponse))
	}
	got := rec.Body.Bytes()
	if !bytes.Equal(got[:len(prefix)], []byte(prefix)) {
		t.Error("response prefix modified")
	}
	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded")
	}
	ev := goldenLastEvent(rw)
	if ev.InputTokens != 1 || ev.OutputTokens != 1 {
		t.Errorf("tokens from prefix: input=%d output=%d, want 1/1", ev.InputTokens, ev.OutputTokens)
	}
	if rw.parseErrors > 0 {
		t.Errorf("parseErrors from truncated sample = %d, want 0", rw.parseErrors)
	}
}

func TestGolden_DroppedEventDoesNotBreakResponse(t *testing.T) {
	upstreamResp := `{"model":"gpt-4o","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	dropRW := &droppingWriter{}
	p := New(upstream.URL, hash.NewWithSalt("golden-salt"), dropRW, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.String() != upstreamResp {
		t.Error("response body modified when writer drops event")
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

type droppingWriter struct{}

func (d *droppingWriter) Enqueue(event writer.StatsEvent) bool { return false }
func (d *droppingWriter) IncrParseErrors()                     {}

func BenchmarkGolden_StreamForwarding(b *testing.B) {
	streamBody := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\ndata: [DONE]\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write(streamBody)
	}))
	defer upstream.Close()

	rw := &goldenTestRW{}
	p := New(upstream.URL, hash.NewWithSalt("bench-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"stream":true}`))
		req.Header.Set("Accept", "text/event-stream")
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
	}
}
