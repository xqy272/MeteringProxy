package credential

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/cliproxy"
	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/hash"
)

type fakeCredentialStore struct {
	rows []db.CredentialHealthRow
}

func (s *fakeCredentialStore) UpsertCredentialHealth(row *db.CredentialHealthRow) error {
	s.rows = append(s.rows, *row)
	return nil
}

func (s *fakeCredentialStore) AllCredentialHealth() ([]db.CredentialHealthRow, error) {
	out := make([]db.CredentialHealthRow, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

func (s *fakeCredentialStore) DeleteStaleCredentialHealth(time.Time) error {
	return nil
}

func TestPollHashesCredentialFieldsWithNamespaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/auth-files" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"auth_files":[{"provider":"claude","auth_type":"api_key","auth_index":1,"label":"primary","key":"provider-key","available":false}]}`))
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
	store := &fakeCredentialStore{}
	h := hash.NewWithSalt("test-salt")
	p := NewPoller(client, store, h, config.CredentialHealthConfig{})
	p.poll()

	if len(store.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(store.rows))
	}
	row := store.rows[0]
	if row.CredentialHash != h.Hash("credential:claude:key:provider-key") {
		t.Fatalf("CredentialHash = %q", row.CredentialHash)
	}
	if row.LabelHash != h.Hash("credential_label:primary") {
		t.Fatalf("LabelHash = %q", row.LabelHash)
	}
	if row.ErrorClass != "credential_unavailable" {
		t.Fatalf("ErrorClass = %q", row.ErrorClass)
	}
}

func TestPollNormalizesCLIProxyAPIv704AuthFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/auth-files" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"files":[{"id":"codex-1","auth_index":"12","type":"codex","provider":"codex","label":"Codex Primary","status":"active","disabled":false,"unavailable":false,"success":9,"failed":1}]}`))
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
	store := &fakeCredentialStore{}
	h := hash.NewWithSalt("test-salt")
	p := NewPoller(client, store, h, config.CredentialHealthConfig{})
	p.poll()

	if len(store.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(store.rows))
	}
	row := store.rows[0]
	if row.Provider != "codex" {
		t.Fatalf("Provider = %q", row.Provider)
	}
	if row.CredentialHash != h.Hash("credential:codex:auth_index:codex:codex:12") {
		t.Fatalf("CredentialHash = %q", row.CredentialHash)
	}
	if row.Status != "ready" || row.ErrorClass != "" {
		t.Fatalf("status/error = %q/%q", row.Status, row.ErrorClass)
	}
	if row.SuccessCount != 9 || row.FailedCount != 1 {
		t.Fatalf("counts = %d/%d", row.SuccessCount, row.FailedCount)
	}
}
