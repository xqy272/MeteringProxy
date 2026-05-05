package proxy

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/hash"
)

func TestProxyNonStreaming_ErrorFieldsRecorded(t *testing.T) {
	upstreamResp := `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 429 {
		t.Errorf("status = %d, want 429", rec.Code)
	}
	if rec.Body.String() != upstreamResp {
		t.Error("response body modified for error response")
	}
	if len(rw.events) == 0 {
		t.Fatal("no event recorded for error response")
	}
	ev := lastEvent(rw)
	if ev.ErrorClass != "quota_exhausted" {
		t.Errorf("error_class = %q, want quota_exhausted", ev.ErrorClass)
	}
	if ev.ErrorType != "insufficient_quota" {
		t.Errorf("error_type = %q, want insufficient_quota", ev.ErrorType)
	}
	if ev.ErrorCode != "insufficient_quota" {
		t.Errorf("error_code = %q, want insufficient_quota", ev.ErrorCode)
	}
	if ev.ErrorMessage != "" {
		t.Errorf("error_message = %q, want empty; provider messages are not persisted", ev.ErrorMessage)
	}
}

func TestProxyStreaming_ErrorFieldsRecorded(t *testing.T) {
	streamBody := "data: {\"error\":{\"message\":\"Rate limit exceeded\",\"type\":\"rate_limit_error\"}}\n\ndata: [DONE]\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(429)
		w.Write([]byte(streamBody))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.String() != streamBody {
		t.Errorf("SSE error response modified: got %q, want %q", rec.Body.String(), streamBody)
	}
	if len(rw.events) == 0 {
		t.Fatal("no event recorded for SSE error response")
	}
	ev := lastEvent(rw)
	if ev.ErrorClass != "rate_limited" {
		t.Errorf("error_class = %q, want rate_limited", ev.ErrorClass)
	}
}

func TestProxyRequest_UpstreamErrorSetsErrorClass(t *testing.T) {
	rw := &testRW{}
	p := New("http://127.0.0.1:1", hash.NewWithSalt("test-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	ev := lastEvent(rw)
	if ev.ErrorClass != "proxy_upstream_error" {
		t.Errorf("error_class = %q, want proxy_upstream_error", ev.ErrorClass)
	}
	if ev.Error != "connection_refused" && ev.Error != "upstream_error" {
		t.Errorf("error = %q, want safe upstream category", ev.Error)
	}
	if strings.Contains(ev.Error, "127.0.0.1") {
		t.Errorf("error leaked upstream address: %q", ev.Error)
	}
}

func TestForwardTransparentErrorSetsMeteringHeader(t *testing.T) {
	rw := &testRW{}
	p := New("http://127.0.0.1:1", hash.NewWithSalt("test-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 502 {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if rec.Header().Get("X-Metering-Proxy") != "1" {
		t.Fatal("missing X-Metering-Proxy header on passthrough error")
	}
	if len(rw.events) != 0 {
		t.Fatalf("passthrough error recorded %d events, want 0", len(rw.events))
	}
}

func TestSafeOperationalErrorRedactsDetails(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8317}, Err: errors.New("connect: connection refused")}
	got := safeOperationalError(err)
	if got != "connection_refused" {
		t.Fatalf("safeOperationalError = %q, want connection_refused", got)
	}
	if strings.Contains(got, "127.0.0.1") || strings.Contains(got, "8317") {
		t.Fatalf("safeOperationalError leaked address: %q", got)
	}
}

func TestProxyStreaming_SSEErrorSamplingOverflow(t *testing.T) {
	// More than 5 SSE payloads should preserve the bounded first samples instead
	// of discarding everything already collected.
	chunks := []string{
		"data: {\"error\":{\"message\":\"err1\"}}\n",
		"data: {\"error\":{\"message\":\"err2\"}}\n",
		"data: {\"error\":{\"message\":\"err3\"}}\n",
		"data: {\"error\":{\"message\":\"err4\"}}\n",
		"data: {\"error\":{\"message\":\"err5\"}}\n",
		"data: {\"error\":{\"message\":\"err6\"}}\n",
		"data: [DONE]\n",
	}
	var fullStream bytes.Buffer
	for _, c := range chunks {
		fullStream.WriteString(c)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(429)
		flusher := w.(http.Flusher)
		for _, c := range chunks {
			w.Write([]byte(c))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.String() != fullStream.String() {
		t.Error("SSE stream body modified after overflow")
	}
	if len(rw.events) == 0 {
		t.Fatal("no event recorded")
	}
	ev := lastEvent(rw)
	if ev.ErrorClass != "rate_limited" {
		t.Fatalf("error_class after overflow = %q, want rate_limited from bounded sample", ev.ErrorClass)
	}
}

func TestProxyNonStreaming_ErrorResponseByteTransparent(t *testing.T) {
	upstreamResp := `{"error":{"message":"Not found","type":"not_found_error"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.String() != upstreamResp {
		t.Error("error response body modified by proxy")
	}
}

func TestProxyNonStreaming_ParseErrorDoesNotBlockForwarding(t *testing.T) {
	// JSON parse error in sample should not affect byte transparency
	upstreamResp := `not json at all, just garbage`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	rw := &testRW{}
	p := New(upstream.URL, hash.NewWithSalt("test-salt"), rw, 2*1024*1024, config.RequestMetadataConfig{InitialBytes: 4096, MaxBytes: 65536, ExtendedModelScan: false})

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Body.String() != upstreamResp {
		t.Error("non-JSON error response body modified")
	}
	// Even though JSON parsing failed, forwarding must continue
	// The event should be recorded without error classification
	if len(rw.events) == 0 {
		t.Fatal("no event recorded for non-JSON error")
	}
}
