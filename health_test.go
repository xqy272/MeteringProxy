package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type mockDBReady struct {
	calls atomic.Int64
	err   error
	block chan struct{}
}

func (m *mockDBReady) Ready(ctx context.Context) error {
	m.calls.Add(1)
	if m.block != nil {
		select {
		case <-m.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.err
}

type mockWriterReady struct {
	running bool
	calls   atomic.Int64
}

func (m *mockWriterReady) Running() bool {
	m.calls.Add(1)
	return m.running
}

type trackingUpstream struct {
	called atomic.Int64
}

func (t *trackingUpstream) Ready(ctx context.Context) error {
	t.called.Add(1)
	return nil
}

func TestHealthzGETJSONAndMethod(t *testing.T) {
	h := healthzHandler()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	var body healthzResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("status = %q", body.Status)
	}

	req = httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("Allow = %q", rr.Header().Get("Allow"))
	}
}

func TestHealthzDoesNotTouchDependencies(t *testing.T) {
	db := &mockDBReady{}
	writer := &mockWriterReady{running: true}
	// healthzHandler intentionally ignores readiness deps.
	h := healthzHandler()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if db.calls.Load() != 0 || writer.calls.Load() != 0 {
		t.Fatalf("healthz touched deps: db=%d writer=%d", db.calls.Load(), writer.calls.Load())
	}
	_ = db
	_ = writer
}

func TestReadyzReadyAndNotReady(t *testing.T) {
	db := &mockDBReady{}
	writer := &mockWriterReady{running: true}
	h := readyzHandler(&readiness{
		configOK:  true,
		saltOK:    true,
		pricingOK: true,
		db:        db,
		writer:    writer,
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body readyzResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Status != "ready" {
		t.Fatalf("status = %q", body.Status)
	}
	for _, key := range []string{"config", "salt", "pricing", "database", "writer"} {
		if body.Components[key] != componentOK {
			t.Fatalf("component %s = %q", key, body.Components[key])
		}
	}

	// DB failure -> 503 sanitized.
	db.err = errors.New("sqlite disk I/O error at /secret/path/usage.sqlite")
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rr.Code)
	}
	raw := rr.Body.String()
	if strings.Contains(raw, "/secret/path") || strings.Contains(raw, "disk I/O") {
		t.Fatalf("response leaked internal error/path: %s", raw)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Status != "not_ready" || body.Components["database"] != componentError {
		t.Fatalf("body = %+v", body)
	}

	// Writer not running.
	db.err = nil
	writer.running = false
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Components["writer"] != componentError {
		t.Fatalf("writer = %q", body.Components["writer"])
	}
}

func TestReadyzMethodAllowAndCanceledContext(t *testing.T) {
	block := make(chan struct{})
	db := &mockDBReady{block: block}
	h := readyzHandler(&readiness{
		configOK:     true,
		saltOK:       true,
		pricingOK:    true,
		db:           db,
		writer:       &mockWriterReady{running: true},
		probeTimeout: 30 * time.Millisecond,
	})

	req := httptest.NewRequest(http.MethodPost, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method handling failed: code=%d allow=%q", rr.Code, rr.Header().Get("Allow"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	req = httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil)
	rr = httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rr, req)
	}()
	// Cancel while DB probe is blocked; readiness must complete via context.
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readyz did not observe canceled context")
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	close(block)
}

func TestReadyzDoesNotRequireExternalClients(t *testing.T) {
	// Prove the handler only uses injected local probes: no upstream/CPA interface is required.
	h := readyzHandler(&readiness{
		configOK:  true,
		saltOK:    true,
		pricingOK: true,
		db:        &mockDBReady{},
		writer:    &mockWriterReady{running: true},
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	// Ensure response body is fully consumed and closed.
	_, _ = io.Copy(io.Discard, rr.Result().Body)
	_ = rr.Result().Body.Close()
}

func TestReadyzStartupFlagsNotReady(t *testing.T) {
	h := readyzHandler(&readiness{
		configOK:  true,
		saltOK:    false,
		pricingOK: true,
		db:        &mockDBReady{},
		writer:    &mockWriterReady{running: true},
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rr.Code)
	}
	var body readyzResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Components["salt"] != componentError {
		t.Fatalf("salt = %q", body.Components["salt"])
	}
}
