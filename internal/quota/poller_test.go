package quota

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/cliproxy"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/hash"
)

type fakeQuotaStore struct {
	rows   []db.QuotaCurrentRow
	events []db.QuotaRefreshEventRow
}

func (s *fakeQuotaStore) UpsertQuotaCurrent(row *db.QuotaCurrentRow) error {
	s.rows = append(s.rows, *row)
	return nil
}

func (s *fakeQuotaStore) AllQuotaCurrent() ([]db.QuotaCurrentRow, error) {
	out := make([]db.QuotaCurrentRow, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

func (s *fakeQuotaStore) InsertQuotaRefreshEvent(row *db.QuotaRefreshEventRow) error {
	s.events = append(s.events, *row)
	return nil
}

func (s *fakeQuotaStore) DeleteStaleQuotaRefreshEvents(time.Time) error {
	return nil
}

func TestGenericAdapterReturnsParseErrorWhenDataMissing(t *testing.T) {
	store := &fakeQuotaStore{}
	p := NewPoller(nil, store, hash.NewWithSalt("test-salt"), config.QuotaConfig{})

	if _, err := (genericAdapter{}).ParseResponse(p, "claude", []byte(`{"ok":true}`), "now", 1); err == nil {
		t.Fatal("ParseResponse error = nil, want missing data error")
	}
}

func TestProcessQuotaEntryUsesNamespacedCredentialHash(t *testing.T) {
	store := &fakeQuotaStore{}
	h := hash.NewWithSalt("test-salt")
	p := NewPoller(nil, store, h, config.QuotaConfig{LowThreshold: 0.2, WarningThreshold: 0.5})

	credHash, err := p.processQuotaEntry("claude", map[string]interface{}{
		"key":              "provider-key",
		"window_key":       "daily",
		"limit_amount":     float64(100),
		"remaining_amount": float64(20),
	}, "2026-05-05T00:00:00Z", 1777939200)
	if err != nil {
		t.Fatalf("processQuotaEntry: %v", err)
	}
	want := h.Hash("quota_credential:claude:key:provider-key")
	if credHash != want {
		t.Fatalf("credHash = %q, want %q", credHash, want)
	}
	if len(store.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(store.rows))
	}
	if store.rows[0].AdapterStatus != "available" {
		t.Fatalf("AdapterStatus = %q", store.rows[0].AdapterStatus)
	}
}

func TestUnsupportedProviderWritesUnsupportedQuotaRow(t *testing.T) {
	store := &fakeQuotaStore{}
	p := NewPoller(nil, store, hash.NewWithSalt("test-salt"), config.QuotaConfig{})

	p.recordUnsupportedProvider("unknown-provider", "2026-05-05T00:00:00Z", 1777939200)
	if len(store.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(store.rows))
	}
	row := store.rows[0]
	if row.AdapterStatus != "unsupported" || row.QuotaSupported != 0 || row.Status != "unsupported" {
		t.Fatalf("row = %#v", row)
	}
	if len(store.events) != 1 || store.events[0].ErrorClass != "quota_unsupported" {
		t.Fatalf("events = %#v, want quota_unsupported refresh diagnostic", store.events)
	}
}

func TestProbeAPICallTreatsCLIProxyAPIBadRequestAsReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/api-call" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"missing method"}`))
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
	p := NewPoller(client, &fakeQuotaStore{}, hash.NewWithSalt("test-salt"), config.QuotaConfig{})
	p.probeAPICall()
	if !p.APICallAvailable() {
		t.Fatal("APICallAvailable = false, want true for reachable CPA api-call endpoint")
	}
	_, status, class, endpoint := p.probeSnapshot()
	if status != http.StatusBadRequest || class != "api_call_bad_request" || endpoint != "/api-call" {
		t.Fatalf("probe = status:%d class:%q endpoint:%q, want 400/api_call_bad_request//api-call", status, class, endpoint)
	}
}

func TestProbeAPICallDetectsCLIProxyAPICurrentContract(t *testing.T) {
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/api-call" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		requestBody = string(body)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"request failed"}`))
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
	p := NewPoller(client, &fakeQuotaStore{}, hash.NewWithSalt("test-salt"), config.QuotaConfig{})
	p.probeAPICall()
	if !p.APICallAvailable() {
		t.Fatal("APICallAvailable = false, want true for accepted api-call request contract")
	}
	_, status, class, _ := p.probeSnapshot()
	if status != http.StatusBadGateway || class != "api_call_probe_reachable" {
		t.Fatalf("probe = status:%d class:%q, want 502/api_call_probe_reachable", status, class)
	}
	if !strings.Contains(requestBody, `"method":"GET"`) || !strings.Contains(requestBody, `"url":"http://127.0.0.1:0/__metering_probe__"`) {
		t.Fatalf("probe request body = %s, want method/url contract", requestBody)
	}
}

func TestRecordRefreshEventPersistsProbeDiagnostics(t *testing.T) {
	store := &fakeQuotaStore{}
	p := NewPoller(nil, store, hash.NewWithSalt("test-salt"), config.QuotaConfig{})
	p.mu.Lock()
	p.apiCallAvail = true
	p.probeStatus = http.StatusBadRequest
	p.probeClass = "api_call_bad_request"
	p.probeEndpoint = "/api-call"
	p.mu.Unlock()

	p.recordRefreshEvent("codex", "", "adapter", "error", "unsupported", 12, "2026-05-05T00:00:00Z", 1777939200)
	if len(store.events) != 1 {
		t.Fatalf("events = %d, want 1", len(store.events))
	}
	got := store.events[0]
	if got.ProbeHTTPStatus != http.StatusBadRequest || got.ProbeErrorClass != "api_call_bad_request" || got.APICallReachable != 1 {
		t.Fatalf("event probe fields = %#v", got)
	}
}
