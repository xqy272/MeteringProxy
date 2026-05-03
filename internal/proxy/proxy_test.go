package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/hash"
	"ai-gateway-metering-proxy/internal/metrics"
	"ai-gateway-metering-proxy/internal/writer"
)

type testRW struct {
	events      []writer.StatsEvent
	parseErrors int64
}

func (t *testRW) Enqueue(ev writer.StatsEvent) bool {
	t.events = append(t.events, ev)
	return true
}

func (t *testRW) IncrParseErrors() {
	t.parseErrors++
}

// helper to get the domain event from the last recorded StatsEvent
func lastEvent(rw *testRW) event.Event {
	if len(rw.events) == 0 {
		return event.Event{}
	}
	return rw.events[len(rw.events)-1].Event
}

func TestReplayReader_FullBodyForwarded(t *testing.T) {
	fullBody := bytes.Repeat([]byte("x"), 20*1024)
	prefix := fullBody[:4096]
	src := io.NopCloser(bytes.NewReader(fullBody[4096:]))
	r := &replayReader{prefix: prefix, src: src}

	var got bytes.Buffer
	n, err := io.Copy(&got, r)
	if err != nil {
		t.Fatalf("copy error: %v", err)
	}
	if n != int64(len(fullBody)) {
		t.Errorf("read %d bytes, want %d", n, len(fullBody))
	}
	if !bytes.Equal(got.Bytes(), fullBody) {
		t.Error("body mismatch; prefix + source not concatenated correctly")
	}
}

func TestReplayReader_ExactPrefix(t *testing.T) {
	r := &replayReader{prefix: []byte("hello world"), src: io.NopCloser(strings.NewReader(" and beyond"))}
	var got bytes.Buffer
	io.Copy(&got, r)
	if got.String() != "hello world and beyond" {
		t.Errorf("got %q", got.String())
	}
}

func TestLimitedBuffer_StopsAtMax(t *testing.T) {
	lb := &limitedBuffer{max: 10}
	n, err := lb.Write([]byte("hello world this is long"))
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	if n != len("hello world this is long") {
		t.Errorf("Write should return full input len %d, got %d", len("hello world this is long"), n)
	}
	if len(lb.Bytes()) != 10 {
		t.Errorf("captured %d bytes, want 10", len(lb.Bytes()))
	}
	if !lb.overflow {
		t.Error("overflow flag should be set")
	}
}

func TestLimitedBuffer_NoOverflow(t *testing.T) {
	lb := &limitedBuffer{max: 100}
	lb.Write([]byte("short"))
	if lb.overflow {
		t.Error("overflow should not be set for small writes")
	}
	if string(lb.Bytes()) != "short" {
		t.Errorf("got %q", lb.Bytes())
	}
}

func TestExtractModel(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{`{"model":"gpt-4o","messages":[]}`, "gpt-4o"},
		{`{"messages":[]}`, ""},
		{``, ""},
		{`{"model":"deepseek-chat","stream":true}`, "deepseek-chat"},
	}
	for _, tc := range tests {
		got := extractModel([]byte(tc.body))
		if got != tc.want {
			t.Errorf("extractModel(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

func TestIsSSEMediaType(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"Text/Event-Stream", true},
		{"TEXT/EVENT-STREAM; BOUNDARY=xx", true},
		{"application/json, text/event-stream", true},
		{"application/json", false},
		{"text/plain", false},
		{"", false},
		{"text/event-streamish", false},
	}
	for _, tc := range tests {
		got := isSSEMediaType(tc.header)
		if got != tc.want {
			t.Errorf("isSSEMediaType(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestStreamFromJSON(t *testing.T) {
	if !streamFromJSON([]byte(`{"stream":true}`)) {
		t.Error("expected true for stream:true")
	}
	if streamFromJSON([]byte(`{"stream":false}`)) {
		t.Error("expected false for stream:false")
	}
	if streamFromJSON([]byte(`{}`)) {
		t.Error("expected false for empty")
	}
	if streamFromJSON([]byte(`{"messages":[{"content":"stream:true"}]}`)) {
		t.Error("stream:true in nested content should not match")
	}
	if streamFromJSON([]byte(``)) {
		t.Error("empty body should return false")
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		auth string
		want string
	}{
		{"Bearer sk-test", "sk-test"},
		{"bearer sk-test", "sk-test"},
		{"BEARER sk-test", "sk-test"},
		{"Token sk-test", "Token sk-test"},
		{"Bearer", "Bearer"},
	}
	for _, tc := range tests {
		if got := bearerToken(tc.auth); got != tc.want {
			t.Errorf("bearerToken(%q) = %q, want %q", tc.auth, got, tc.want)
		}
	}
}

func TestProxyNonStreaming_SmallResponse(t *testing.T) {
	upstreamResp := `{"model":"gpt-4o","usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("OpenAI-Request-ID", "req_upstream")
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	hasher := hash.NewWithSalt("test-salt")
	p := New(upstream.URL, hasher, rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "bearer sk-test-key")
	req.Header.Set("X-Request-ID", "req_client")
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != upstreamResp {
		t.Errorf("response body modified")
	}
	if rec.Header().Get("X-Metering-Proxy") != "1" {
		t.Error("missing X-Metering-Proxy header")
	}
	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded")
	}
	ev := lastEvent(rw)
	if ev.InputTokens != 100 || ev.OutputTokens != 200 {
		t.Errorf("tokens: input=%d output=%d, want 100/200", ev.InputTokens, ev.OutputTokens)
	}
	if ev.APIKeyHash == "" || ev.APIKeyHash == "sk-test-key" {
		t.Error("api_key_hash should be hashed, not empty or plaintext")
	}
	if ev.APIKeyHash != hasher.Hash("sk-test-key") {
		t.Error("api_key_hash should hash the bearer token case-insensitively")
	}
	if ev.ID != "req_upstream" {
		t.Errorf("request_id = %q, want upstream request id", ev.ID)
	}
	if ev.EndpointProfile != "chat_completions" {
		t.Errorf("endpoint_profile = %q, want chat_completions", ev.EndpointProfile)
	}
	if ev.CaptureMode != event.CaptureUsageMetered {
		t.Errorf("capture_mode = %q, want usage_metered", ev.CaptureMode)
	}
}

func TestProxyPreservesGzipResponseAndDoesNotInjectAcceptEncoding(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write([]byte(`{"ok":true}`)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	var upstreamAcceptEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(compressed.Bytes())
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if upstreamAcceptEncoding != "" {
		t.Fatalf("proxy injected Accept-Encoding = %q, want empty", upstreamAcceptEncoding)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", rec.Header().Get("Content-Encoding"))
	}
	if !bytes.Equal(rec.Body.Bytes(), compressed.Bytes()) {
		t.Fatal("gzip response bytes were modified")
	}
}

func TestProxyRecordsTTFBSeparatelyFromTotalLatency(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"gpt-4o","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded")
	}
	ev := lastEvent(rw)
	if ev.TTFBMs <= 0 {
		t.Fatalf("TTFBMs = %d, want > 0", ev.TTFBMs)
	}
	if ev.LatencyMs < ev.TTFBMs {
		t.Fatalf("LatencyMs = %d, want >= TTFBMs %d", ev.LatencyMs, ev.TTFBMs)
	}
}

func TestProxyNonStreaming_LargeResponseNotTruncated(t *testing.T) {
	bodyBytes := make([]byte, 3*1024*1024)
	for i := range bodyBytes {
		bodyBytes[i] = 'x'
	}
	fullResp := `{"model":"gpt-4o","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}` + string(bodyBytes)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fullResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.Len() != len(fullResp) {
		t.Errorf("response length = %d, want %d; body was truncated", rec.Body.Len(), len(fullResp))
	}
	if len(rw.events) == 0 || lastEvent(rw).InputTokens == 0 {
		t.Error("usage extraction failed for large response")
	}
	if rw.parseErrors > 0 {
		t.Errorf("parseErrors = %d, want 0; truncated sample should not count as parse error", rw.parseErrors)
	}
}

func TestProxyNonStreaming_TruncatedJSONNoParseError(t *testing.T) {
	largeJSON := `{"model":"gpt-4o","choices":[{"message":{"content":"` + strings.Repeat("x", 5000) + `"}}],"usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300}}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(largeJSON))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 512)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.Len() != len(largeJSON) {
		t.Errorf("response length = %d, want %d", rec.Body.Len(), len(largeJSON))
	}
	if rw.parseErrors > 0 {
		t.Errorf("parseErrors = %d, want 0; truncated JSON sample should not count as parse error", rw.parseErrors)
	}
}

func TestProxyStreaming_ByteTransparent(t *testing.T) {
	rawLines := "data: {\"x\":\"a\"}\r\ndata: [DONE]\r\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(rawLines))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.String() != rawLines {
		t.Errorf("SSE response modified: got %q, want %q", rec.Body.String(), rawLines)
	}
}

func TestProxyStreaming_SSEAcrossChunks(t *testing.T) {
	chunks := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\" World\"}}]}\n",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usa",
		"ge\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n",
		"data: [DONE]\n",
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

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded; cross-chunk SSE parse failed")
	}
	ev := lastEvent(rw)
	if ev.InputTokens != 10 || ev.OutputTokens != 2 {
		t.Errorf("input=%d output=%d, want 10/2", ev.InputTokens, ev.OutputTokens)
	}
}

func TestProxyStreaming_ContextCancellationRecords(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n"))
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Error("no usage event recorded on context cancellation")
	}
}

func TestProxyStreaming_LongLineDoesNotBlockForwarding(t *testing.T) {
	longContent := strings.Repeat("x", 300*1024)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"" + longContent + "\"}}]}\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":300,\"total_tokens\":305}}\n" +
		"data: [DONE]\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(stream))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)
	beforeSkips := metrics.SSELineSkips()

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.Len() != len(stream) {
		t.Errorf("response length = %d, want %d; long line blocked forwarding", rec.Body.Len(), len(stream))
	}
	if len(rw.events) == 0 || lastEvent(rw).InputTokens != 5 {
		t.Error("usage extraction should still work for subsequent normal-sized line")
	}
	if got := metrics.SSELineSkips() - beforeSkips; got != 1 {
		t.Errorf("SSELineSkips delta = %d, want 1 for oversized line", got)
	}
}

func TestProxyStreaming_LongLineAcrossChunksCountsOneSkip(t *testing.T) {
	first := "data: " + strings.Repeat("x", 200*1024)
	second := strings.Repeat("y", 120*1024) + "\n"
	usage := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3,\"total_tokens\":8}}\n"
	done := "data: [DONE]\n"
	stream := first + second + usage + done

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range []string{first, second, usage, done} {
			w.Write([]byte(chunk))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)
	beforeSkips := metrics.SSELineSkips()

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.String() != stream {
		t.Error("stream response body modified")
	}
	if got := metrics.SSELineSkips() - beforeSkips; got != 1 {
		t.Errorf("SSELineSkips delta = %d, want 1 for one oversized fragmented line", got)
	}
	if len(rw.events) == 0 || lastEvent(rw).InputTokens != 5 || lastEvent(rw).OutputTokens != 3 {
		t.Error("usage extraction should continue after oversized fragmented line")
	}
}

func TestProxyRequest_UpstreamError(t *testing.T) {
	rw := &testRW{}
	p := New("http://127.0.0.1:1", hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "127.0.0.1") || strings.Contains(rec.Body.String(), "connect") {
		t.Fatalf("client error body leaked upstream detail: %q", rec.Body.String())
	}
	if len(rw.events) == 0 || lastEvent(rw).Error == "" {
		t.Error("error event should be recorded with error string")
	}
	if lastEvent(rw).Error != event.ReasonUpstreamError {
		t.Errorf("event error = %q, want %q", lastEvent(rw).Error, event.ReasonUpstreamError)
	}
}

func TestProxyResponsesAPI_NonStreaming(t *testing.T) {
	upstreamResp := `{"model":"gpt-5.4-mini","usage":{"input_tokens":371,"output_tokens":43,"total_tokens":414}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	ev := lastEvent(rw)
	if ev.InputTokens != 371 || ev.OutputTokens != 43 {
		t.Errorf("input=%d output=%d, want 371/43", ev.InputTokens, ev.OutputTokens)
	}
	if ev.EndpointProfile != "responses" {
		t.Errorf("endpoint_profile = %q, want responses", ev.EndpointProfile)
	}
}

func TestCountingReader(t *testing.T) {
	cr := &countingReader{r: io.NopCloser(strings.NewReader("hello world"))}
	buf := make([]byte, 5)
	n, _ := cr.Read(buf)
	if n != 5 || cr.bytesRead != 5 {
		t.Errorf("after first read: n=%d bytesRead=%d", n, cr.bytesRead)
	}
	n, _ = cr.Read(buf)
	if n != 5 || cr.bytesRead != 10 {
		t.Errorf("after second read: n=%d bytesRead=%d", n, cr.bytesRead)
	}
	n, _ = cr.Read(buf)
	if n != 1 || cr.bytesRead != 11 {
		t.Errorf("after final read: n=%d bytesRead=%d", n, cr.bytesRead)
	}
	cr.Close()
}

func TestStreamDetection_ResponseContentType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "Text/Event-Stream; charset=utf-8")
		w.Write([]byte("data: {}\n"))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	if !lastEvent(rw).Stream {
		t.Error("stream should be true; response Content-Type was text/event-stream")
	}
}

func TestProxyNonStreaming_ParseErrorIncremented(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"gpt-4o","usage":{"prompt_to`))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 10*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rw.parseErrors == 0 {
		t.Error("parse error should be incremented for broken JSON within sample limit")
	}
}

func TestProxyNonStreaming_NoParseErrorOnValidJSONNoUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 10*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rw.parseErrors != 0 {
		t.Errorf("parseErrors = %d, want 0; valid JSON without usage is not an error", rw.parseErrors)
	}
}

func TestStreamDetection_UsesJSON(t *testing.T) {
	rw := &testRW{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {}\n"))
	}))
	defer upstream.Close()

	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	if !lastEvent(rw).Stream {
		t.Error("stream should be true; JSON-based detection failed")
	}
}

func TestStreamDetection_ResponseJSONOverridesStreamTrue(t *testing.T) {
	upstreamResp := `{"model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.String() != upstreamResp {
		t.Error("response body modified")
	}
	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	if lastEvent(rw).Stream {
		t.Error("stream should be false; response Content-Type is application/json")
	}
}

func TestStreamDetection_ResponseSSEPriority(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "Text/Event-Stream; charset=utf-8")
		w.Write([]byte("data: {}\n"))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	if !lastEvent(rw).Stream {
		t.Error("stream should be true; response Content-Type is text/event-stream")
	}
}

func TestProxyStreaming_ResponsesAPIAcrossChunks(t *testing.T) {
	chunks := []string{
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\" World\"}\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-5.4-mini-2026-03-17\",\"usage\":{\"input_tokens\":371,\"output_tokens\":43,\"output_tokens_details\":{\"reasoning_tokens\":5},\"input_tokens_details\":{\"cached_tokens\":10},\"total_tokens\":414}}}\n",
		"data: [DONE]\n",
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

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no usage event recorded; Responses SSE parse failed")
	}
	ev := lastEvent(rw)
	if ev.InputTokens != 371 {
		t.Errorf("input_tokens = %d, want 371", ev.InputTokens)
	}
	if ev.OutputTokens != 43 {
		t.Errorf("output_tokens = %d, want 43", ev.OutputTokens)
	}
	if ev.ReasoningTokens != 5 {
		t.Errorf("reasoning_tokens = %d, want 5", ev.ReasoningTokens)
	}
	if ev.CachedTokens != 10 {
		t.Errorf("cached_tokens = %d, want 10", ev.CachedTokens)
	}
	if ev.TotalTokens != 414 {
		t.Errorf("total_tokens = %d, want 414", ev.TotalTokens)
	}
	if ev.ModelReturned != "gpt-5.4-mini-2026-03-17" {
		t.Errorf("model_returned = %q, want gpt-5.4-mini-2026-03-17", ev.ModelReturned)
	}
}

func TestStreamDetection_EmptyContentTypeFallsBack(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ",,,garbled--not/valid,,,")
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	if !lastEvent(rw).Stream {
		t.Error("stream should be true; unparseable Content-Type falls back to request stream:true")
	}
}

func TestKillSwitch_DisablesMetering(t *testing.T) {
	upstreamResp := `{"model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)
	p.SetMeteringEnabled(false)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != upstreamResp {
		t.Error("response body modified under kill switch")
	}
	if len(rw.events) != 0 {
		t.Errorf("expected 0 events under kill switch, got %d", len(rw.events))
	}
}

func TestKillSwitch_StillForwards(t *testing.T) {
	upstreamResp := `{"model":"gpt-4o","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)
	p.SetMeteringEnabled(false)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Metering-Proxy") != "1" {
		t.Error("X-Metering-Proxy header should still be set under kill switch")
	}
	if len(rw.events) != 0 {
		t.Error("no events should be recorded when metering is disabled")
	}
}

func TestKillSwitch_StreamPathFlushes(t *testing.T) {
	streamBody := "data: {\"x\":1}\n\ndata: [DONE]\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(streamBody))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)
	p.SetMeteringEnabled(false)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.String() != streamBody {
		t.Error("stream response body modified under kill switch")
	}
	if !rec.Flushed {
		t.Error("stream response was not flushed under kill switch")
	}
	if len(rw.events) != 0 {
		t.Error("no events should be recorded when metering is disabled")
	}
}

func TestPassthroughProfile_NoMetering(t *testing.T) {
	upstreamResp := `{"data":"ok"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	// GET /v1/models is passthrough
	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(rw.events) != 0 {
		t.Errorf("expected 0 events for passthrough, got %d", len(rw.events))
	}
}

func TestCaptureOutcome_Failed_OnParseError(t *testing.T) {
	// Non-streaming response with broken JSON within sample limit.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json at all`))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 10*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	ev := lastEvent(rw)
	if ev.CaptureOutcome != event.OutcomeFailed {
		t.Errorf("capture_outcome = %q, want %q", ev.CaptureOutcome, event.OutcomeFailed)
	}
	if ev.CaptureReason != event.ReasonParseError {
		t.Errorf("capture_reason = %q, want %q", ev.CaptureReason, event.ReasonParseError)
	}
}

func TestCaptureOutcome_Skipped_WhenNoUsageInResponse(t *testing.T) {
	// Non-streaming response with valid JSON but no usage field.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 10*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	ev := lastEvent(rw)
	if ev.CaptureOutcome != event.OutcomeSkipped {
		t.Errorf("capture_outcome = %q, want %q", ev.CaptureOutcome, event.OutcomeSkipped)
	}
	if ev.CaptureReason != event.ReasonUsageNotPresent {
		t.Errorf("capture_reason = %q, want %q", ev.CaptureReason, event.ReasonUsageNotPresent)
	}
}

func TestCaptureOutcome_Skipped_StreamWithoutUsage(t *testing.T) {
	// SSE stream that has data but no usage block.
	streamBody := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\ndata: [DONE]\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(streamBody))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	ev := lastEvent(rw)
	if ev.CaptureOutcome != event.OutcomeSkipped {
		t.Errorf("capture_outcome = %q, want %q (no usage in stream)", ev.CaptureOutcome, event.OutcomeSkipped)
	}
	if ev.CaptureReason != event.ReasonUsageNotPresent {
		t.Errorf("capture_reason = %q, want %q", ev.CaptureReason, event.ReasonUsageNotPresent)
	}
}

func TestUsageRawJSON_TruncatedInEvent(t *testing.T) {
	largeUsage := `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"debug":"` + strings.Repeat("x", 6000) + `"}`
	upstreamResp := `{"model":"gpt-4o","usage":` + largeUsage + `}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 16*1024)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	ev := lastEvent(rw)
	if !ev.UsageRawTruncated {
		t.Fatal("UsageRawTruncated = false, want true")
	}
	if len(ev.UsageRawJSON) != 4*1024 {
		t.Fatalf("UsageRawJSON length = %d, want 4096", len(ev.UsageRawJSON))
	}
	if !strings.Contains(ev.UsageRawJSON, `"prompt_tokens":10`) {
		t.Errorf("UsageRawJSON does not contain usage subset prefix: %.80q", ev.UsageRawJSON)
	}
}
