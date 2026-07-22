package webui

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/report"
	"ai-gateway-metering-proxy/internal/store"
	"ai-gateway-metering-proxy/internal/writer"
)

func decodeAPIError(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type=%q, want application/json; body=%s", ct, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control=%q, want no-store", cc)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error body: %v body=%s", err, rec.Body.String())
	}
	if payload.Error.Code == "" || payload.Error.Message == "" {
		t.Fatalf("empty error payload: %+v body=%s", payload, rec.Body.String())
	}
	return payload.Error.Code, payload.Error.Message
}

func TestAPIExactRoutingNearMisses(t *testing.T) {
	s, _ := newTestServer(t, "/metering")
	// Near-miss paths must not invoke models (or any other API).
	paths := []string{
		"/metering/api/models/",
		"/metering/api/models/extra",
		"/metering/api/not-found",
		"/metering/api/",
		"/metering/api",
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if path == "/metering/api" {
			// Outside apiPrefix (/metering/api/); treated as static/index path, not API JSON.
			if rec.Code == http.StatusOK && strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
				continue
			}
			// File server may 404; either way it must not return API models JSON array success contract.
			if rec.Code == 200 && strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "[") {
				t.Fatalf("%s returned array-like body unexpectedly: %s", path, rec.Body.String())
			}
			continue
		}
		if !strings.HasPrefix(path, "/metering/api/") {
			continue
		}
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		code, _ := decodeAPIError(t, rec)
		if code != "not_found" {
			t.Fatalf("%s code=%q", path, code)
		}
	}

	// Nested path that ends with /api/models must not hit API under base /metering.
	req := httptest.NewRequest(http.MethodGet, "/metering/x/api/models", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	// Static handler path; must not be models API JSON array.
	body := strings.TrimSpace(rec.Body.String())
	if rec.Code == 200 && strings.HasPrefix(body, "[") {
		t.Fatalf("/metering/x/api/models looked like models API success: %s", body)
	}
}

func TestAPIRouteMethodsAllowAndNoSideEffects(t *testing.T) {
	database, err := openTempDB(t)
	if err != nil {
		t.Fatal(err)
	}
	bw := newTestWriter(t, database)
	stub := &stubModelsReporter{}
	s := New(stub, bw, "/metering", DiagnosticsReaders{Quota: database, Obs: database})
	cred := &countingCredPoller{}
	quota := &countingQuotaPoller{}
	s.SetCredPoller(cred)
	s.SetQuotaPoller(quota)

	type tc struct {
		method string
		path   string
		want   int
		allow  string
		code   string
	}
	cases := []tc{
		{http.MethodPost, "/metering/api/models", 405, "GET", "method_not_allowed"},
		{http.MethodPut, "/metering/api/overview", 405, "GET", "method_not_allowed"},
		{http.MethodDelete, "/metering/api/keys", 405, "GET", "method_not_allowed"},
		{http.MethodGet, "/metering/api/cpa/auth/refresh", 405, "POST", "method_not_allowed"},
		{http.MethodGet, "/metering/api/cpa/cooldown/reset", 405, "POST", "method_not_allowed"},
		{http.MethodGet, "/metering/api/provider-quota/refresh", 405, "POST", "method_not_allowed"},
		{http.MethodGet, "/metering/api/quota/refresh", 405, "POST", "method_not_allowed"},
		{http.MethodPost, "/metering/api/cpa/auth", 405, "GET", "method_not_allowed"},
		{http.MethodGet, "/metering/api/health", 200, "", ""},
		{http.MethodPost, "/metering/api/cpa/auth/refresh", 200, "", ""},
	}
	for _, tc := range cases {
		beforeCred, beforeQuota, beforeReset := cred.refreshN, quota.refreshN, cred.resetN
		beforeModels := stub.calls
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s %s status=%d want=%d body=%s", tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
		}
		if tc.want == 405 {
			if got := rec.Header().Get("Allow"); got != tc.allow {
				t.Fatalf("%s %s Allow=%q want %q", tc.method, tc.path, got, tc.allow)
			}
			code, _ := decodeAPIError(t, rec)
			if code != tc.code {
				t.Fatalf("%s %s code=%q", tc.method, tc.path, code)
			}
			if cred.refreshN != beforeCred || quota.refreshN != beforeQuota || cred.resetN != beforeReset {
				t.Fatalf("%s %s triggered poller side effects", tc.method, tc.path)
			}
			if stub.calls != beforeModels {
				t.Fatalf("%s %s called models reporter", tc.method, tc.path)
			}
		}
	}
	if cred.refreshN != 1 {
		t.Fatalf("successful refresh calls=%d", cred.refreshN)
	}
}

func TestAPIStrictQueryValidation(t *testing.T) {
	database, err := openTempDB(t)
	if err != nil {
		t.Fatal(err)
	}
	bw := newTestWriter(t, database)
	stub := &stubModelsReporter{
		out:           []report.ModelReport{{Model: "m", RequestCount: 1}},
		timeseriesOut: []report.TimeseriesReport{{Count: 2}},
		requestsOut:   []report.RequestReport{{Status: 200}},
		issuesOut:     report.IssuesReport{Range: "ok"},
		errorsOut:     report.ErrorsReport{Source: "ok"},
		keysOut:       []report.KeyReport{{RequestCount: 3}},
	}
	s := New(stub, bw, "/metering", DiagnosticsReaders{Quota: database, Obs: database})

	type tc struct {
		path       string
		wantStatus int
		wantCode   string
		// reporter call counters after request
		check func(t *testing.T)
	}
	cases := []tc{
		{
			path: "/metering/api/models?range=yesterday", wantStatus: 400, wantCode: "invalid_range",
			check: func(t *testing.T) {
				if stub.calls != 0 {
					t.Fatalf("models calls=%d", stub.calls)
				}
			},
		},
		{
			path: "/metering/api/models?range=24h", wantStatus: 200, wantCode: "",
			check: func(t *testing.T) {
				if stub.calls != 1 {
					t.Fatalf("models calls=%d", stub.calls)
				}
			},
		},
		{
			path: "/metering/api/timeseries?bucket=15m", wantStatus: 400, wantCode: "invalid_filter",
			check: func(t *testing.T) {
				if stub.timeseriesCalls != 0 {
					t.Fatalf("timeseries calls=%d", stub.timeseriesCalls)
				}
			},
		},
		{
			path: "/metering/api/timeseries?bucket=1h", wantStatus: 200, wantCode: "",
			check: func(t *testing.T) {
				if stub.timeseriesCalls != 1 || stub.timeseriesFilter.BucketMin != 60 {
					t.Fatalf("timeseries calls=%d filter=%+v", stub.timeseriesCalls, stub.timeseriesFilter)
				}
			},
		},
		{
			path: "/metering/api/timeseries", wantStatus: 200, wantCode: "",
			check: func(t *testing.T) {
				if stub.timeseriesCalls != 2 || stub.timeseriesFilter.BucketMin != 60 {
					t.Fatalf("default bucket calls=%d filter=%+v", stub.timeseriesCalls, stub.timeseriesFilter)
				}
			},
		},
		{
			path: "/metering/api/requests?limit=0", wantStatus: 400, wantCode: "invalid_filter",
			check: func(t *testing.T) {
				if stub.requestsCalls != 0 {
					t.Fatalf("requests calls=%d", stub.requestsCalls)
				}
			},
		},
		{
			path: "/metering/api/requests?limit=-1", wantStatus: 400, wantCode: "invalid_filter",
			check: func(t *testing.T) {
				if stub.requestsCalls != 0 {
					t.Fatalf("requests calls=%d", stub.requestsCalls)
				}
			},
		},
		{
			path: "/metering/api/requests?limit=501", wantStatus: 400, wantCode: "invalid_filter",
			check: func(t *testing.T) {
				if stub.requestsCalls != 0 {
					t.Fatalf("requests calls=%d", stub.requestsCalls)
				}
			},
		},
		{
			path: "/metering/api/requests?limit=abc", wantStatus: 400, wantCode: "invalid_filter",
			check: func(t *testing.T) {
				if stub.requestsCalls != 0 {
					t.Fatalf("requests calls=%d", stub.requestsCalls)
				}
			},
		},
		{
			path: "/metering/api/requests?limit=25", wantStatus: 200, wantCode: "",
			check: func(t *testing.T) {
				if stub.requestsCalls != 1 || stub.requestsFilter.Limit != 25 {
					t.Fatalf("requests calls=%d filter=%+v", stub.requestsCalls, stub.requestsFilter)
				}
			},
		},
		{
			path: "/metering/api/requests", wantStatus: 200, wantCode: "",
			check: func(t *testing.T) {
				if stub.requestsCalls != 2 || stub.requestsFilter.Limit != 100 {
					t.Fatalf("default limit calls=%d filter=%+v", stub.requestsCalls, stub.requestsFilter)
				}
			},
		},
		{
			path: "/metering/api/requests?status=ok", wantStatus: 400, wantCode: "invalid_filter",
			check: func(t *testing.T) {
				if stub.requestsCalls != 2 {
					t.Fatalf("requests calls=%d after invalid status", stub.requestsCalls)
				}
			},
		},
		{
			path: "/metering/api/requests?status=4xx", wantStatus: 200, wantCode: "",
			check: func(t *testing.T) {
				if stub.requestsCalls != 3 || stub.requestsFilter.StatusMin != 400 || stub.requestsFilter.StatusMax != 500 {
					t.Fatalf("status filter=%+v calls=%d", stub.requestsFilter, stub.requestsCalls)
				}
			},
		},
		{
			path: "/metering/api/errors?nonzero=yes", wantStatus: 400, wantCode: "invalid_filter",
			check: func(t *testing.T) {
				// Errors stub has no call counter; rely on status.
			},
		},
		{
			path: "/metering/api/errors?nonzero=false", wantStatus: 200, wantCode: "",
		},
		{
			path: "/metering/api/errors?nonzero=true", wantStatus: 200, wantCode: "",
		},
		{
			path: "/metering/api/issues?limit=101", wantStatus: 400, wantCode: "invalid_filter",
			check: func(t *testing.T) {
				if stub.issuesCalls != 0 {
					t.Fatalf("issues calls=%d", stub.issuesCalls)
				}
			},
		},
		{
			path: "/metering/api/issues?limit=15", wantStatus: 200, wantCode: "",
			check: func(t *testing.T) {
				if stub.issuesCalls != 1 || stub.issuesFilter.Limit != 15 {
					t.Fatalf("issues filter=%+v", stub.issuesFilter)
				}
			},
		},
		{
			path: "/metering/api/models?key_hash=NOTHEX", wantStatus: 400, wantCode: "invalid_key_hash",
			check: func(t *testing.T) {
				if stub.calls != 1 {
					t.Fatalf("models calls=%d after invalid key", stub.calls)
				}
			},
		},
		{
			path: "/metering/api/models?key_hash=unknown", wantStatus: 200, wantCode: "",
			check: func(t *testing.T) {
				if stub.calls != 2 || stub.filter.KeyHash != "unknown" {
					t.Fatalf("unknown key filter=%+v calls=%d", stub.filter, stub.calls)
				}
			},
		},
		{
			path: "/metering/api/keys", wantStatus: 200, wantCode: "",
			check: func(t *testing.T) {
				if stub.keysCalls != 1 {
					t.Fatalf("keys calls=%d", stub.keysCalls)
				}
			},
		},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != tc.wantStatus {
			t.Fatalf("%s status=%d want=%d body=%s", tc.path, rec.Code, tc.wantStatus, rec.Body.String())
		}
		if tc.wantStatus >= 400 {
			code, _ := decodeAPIError(t, rec)
			if code != tc.wantCode {
				t.Fatalf("%s code=%q want %q", tc.path, code, tc.wantCode)
			}
		} else if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s Content-Type=%q", tc.path, ct)
		}
		if tc.check != nil {
			tc.check(t)
		}
	}

	// /api/keys remains a bare array.
	req := httptest.NewRequest(http.MethodGet, "/metering/api/keys", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var keys []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &keys); err != nil {
		t.Fatalf("keys not array: %v body=%s", err, rec.Body.String())
	}
}

func TestAPISanitizedReportErrors(t *testing.T) {
	database, err := openTempDB(t)
	if err != nil {
		t.Fatal(err)
	}
	bw := newTestWriter(t, database)
	secret := `SQL error: no such table request_usage at C:\secret\path\usage.sqlite`
	stub := &stubModelsReporter{err: errors.New(secret), summaryErr: errors.New(secret)}
	s := New(stub, bw, "/metering", DiagnosticsReaders{Quota: database, Obs: database})

	for _, path := range []string{"/metering/api/models", "/metering/api/summary"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		code, msg := decodeAPIError(t, rec)
		if code != "report_query_failed" {
			t.Fatalf("%s code=%q", path, code)
		}
		body := rec.Body.String()
		if strings.Contains(body, secret) || strings.Contains(body, `C:\secret`) || strings.Contains(body, "usage.sqlite") || strings.Contains(body, "no such table") {
			t.Fatalf("%s leaked internal error: %s", path, body)
		}
		if !strings.Contains(msg, "failed to load") {
			t.Fatalf("%s message=%q", path, msg)
		}
	}
}

func TestWriteJSONLogsEncodeFailure(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	rec := httptest.NewRecorder()
	writeJSON(rec, failJSON{})
	if !strings.Contains(buf.String(), "writeJSON encode/write failed") {
		t.Fatalf("expected encode failure log, got %q", buf.String())
	}
}

type failJSON struct{}

func (failJSON) MarshalJSON() ([]byte, error) {
	return nil, errors.New("encode boom")
}

type countingCredPoller struct {
	refreshN int
	resetN   int
}

func (p *countingCredPoller) Snapshot() ([]db.CredentialHealthRow, time.Time) {
	return nil, time.Time{}
}
func (p *countingCredPoller) Refresh() { p.refreshN++ }
func (p *countingCredPoller) ResetCooldown() error {
	p.resetN++
	return nil
}

type countingQuotaPoller struct {
	refreshN int
}

func (p *countingQuotaPoller) Snapshot() ([]db.QuotaCurrentRow, time.Time, bool) {
	return nil, time.Time{}, false
}
func (p *countingQuotaPoller) APICallAvailable() bool { return false }
func (p *countingQuotaPoller) Refresh()               { p.refreshN++ }

func openTempDB(t *testing.T) (*db.DB, error) {
	t.Helper()
	database, err := db.Open(t.TempDir() + "/test.sqlite")
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { database.Close() })
	return database, nil
}

func newTestWriter(t *testing.T, database *db.DB) *writer.BatchWriter {
	t.Helper()
	bw := writer.New(store.NewEventSink(database), 100, 10, time.Nanosecond)
	bw.Start()
	t.Cleanup(func() { bw.Stop() })
	return bw
}
