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

func TestFetchAuthFilesDecodesCredentialErrorObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{"id":"codex-1","type":"codex","provider":"codex","status":"error","success":12,"failed":1,"error":{"type":"usage_limit_reached","code":"quota_window","message":"The usage limit has been reached","resets_in_seconds":7200}}]}`))
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
	entry := resp.AuthFiles[0]
	if entry.ErrorClass != "usage_limit_reached" || entry.ErrorType != "usage_limit_reached" || entry.ErrorCode != "quota_window" {
		t.Fatalf("entry error fields = %#v", entry)
	}
	if entry.StatusMessage != "The usage limit has been reached" || entry.ErrorMessage != "The usage limit has been reached" {
		t.Fatalf("entry error message = %#v", entry)
	}
}

func TestFetchAuthFilesDecodesRuntimeHealthFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{
			"id":"codex-1",
			"type":"codex",
			"provider":"codex",
			"status":"error",
			"status_message":"quota exhausted",
			"next_retry_after":"2026-05-21T18:00:00Z",
			"recent_requests":[
				{"time":"17:20-17:30","success":3,"failed":1},
				{"time":"17:30-17:40","success":2,"failed":0}
			],
			"id_token":{"plan_type":"plus"}
		}]}`))
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
	entry := resp.AuthFiles[0]
	if entry.Plan != "plus" {
		t.Fatalf("Plan = %q, want plus", entry.Plan)
	}
	if entry.RecentSuccessCount != 5 || entry.RecentFailedCount != 1 {
		t.Fatalf("recent counts = %d/%d, want 5/1", entry.RecentSuccessCount, entry.RecentFailedCount)
	}
	if entry.NextRetryAfterUnix != 1779386400 {
		t.Fatalf("NextRetryAfterUnix = %d, want 1779386400", entry.NextRetryAfterUnix)
	}
}

func TestFetchAuthFilesIgnoresCLIProxyAPIv709ProjectID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"files": [{
				"id": "codex-1",
				"project_id": "proj_abc",
				"auth_index": "12",
				"name": "codex@example.com",
				"type": "codex",
				"provider": "codex",
				"status": "active",
				"disabled": false,
				"unavailable": false,
				"success": 7,
				"failed": 2
			}]
		}`))
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
	if entry.ID != "codex-1" || entry.Provider != "codex" || entry.AuthIndex != "12" || !entry.Available {
		t.Fatalf("entry = %#v", entry)
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
