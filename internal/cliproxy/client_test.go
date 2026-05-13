package cliproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchUsageQueueDecodesRawRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/usage-queue" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("count") != "2" {
			t.Fatalf("count = %q", r.URL.Query().Get("count"))
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"request_id":"req-1"},"not-json"]`))
	}))
	defer server.Close()

	client, err := NewClient(CLIProxyConfig{
		BaseURL: server.URL + "/v0/management",
		Key:     "secret",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	records, err := client.FetchUsageQueue(2)
	if err != nil {
		t.Fatalf("FetchUsageQueue: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
	if string(records[0]) != `{"request_id":"req-1"}` {
		t.Fatalf("record[0] = %s", records[0])
	}
	if string(records[1]) != "not-json" {
		t.Fatalf("record[1] = %s", records[1])
	}
}

func TestFetchAuthFilesDecodesCLIProxyAPIv704FilesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/auth-files" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{"id":"codex-1","auth_index":"12","name":"codex@example.com","type":"codex","provider":"codex","label":"Codex Primary","status":"active","disabled":false,"unavailable":false,"success":7,"failed":2,"source":"memory"}]}`))
	}))
	defer server.Close()

	client, err := NewClient(CLIProxyConfig{
		BaseURL: server.URL + "/v0/management",
		Key:     "secret",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := client.FetchAuthFiles()
	if err != nil {
		t.Fatalf("FetchAuthFiles: %v", err)
	}
	if len(resp.AuthFiles) != 1 {
		t.Fatalf("len(AuthFiles) = %d, want 1", len(resp.AuthFiles))
	}
	entry := resp.AuthFiles[0]
	if entry.ID != "codex-1" || entry.Provider != "codex" || entry.AuthType != "codex" || entry.AuthIndex != "12" {
		t.Fatalf("entry identity = %#v", entry)
	}
	if entry.SuccessCount != 7 || entry.FailedCount != 2 || !entry.Available {
		t.Fatalf("entry counters/status = %#v", entry)
	}
}

func TestFetchAuthFilesStillDecodesLegacyAuthFilesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth_files":[{"provider":"claude","auth_type":"api_key","auth_index":1,"label":"primary","success_count":3,"failed_count":1,"available":false,"key":"provider-key"}]}`))
	}))
	defer server.Close()

	client, err := NewClient(CLIProxyConfig{
		BaseURL: server.URL + "/v0/management",
		Key:     "secret",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := client.FetchAuthFiles()
	if err != nil {
		t.Fatalf("FetchAuthFiles: %v", err)
	}
	if len(resp.AuthFiles) != 1 {
		t.Fatalf("len(AuthFiles) = %d, want 1", len(resp.AuthFiles))
	}
	entry := resp.AuthFiles[0]
	if entry.AuthIndex != "1" || entry.SuccessCount != 3 || entry.FailedCount != 1 || entry.Available {
		t.Fatalf("entry = %#v", entry)
	}
}
