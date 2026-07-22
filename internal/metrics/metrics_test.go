package metrics

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func resetForTest() {
	atomic.StoreInt64(&sseLineSkips, 0)
	atomic.StoreInt64(&queueDepth, 0)
	atomic.StoreInt64(&droppedEvents, 0)
	atomic.StoreInt64(&parseErrors, 0)
	atomic.StoreInt64(&dbWriteErrors, 0)
	atomic.StoreInt64(&latencySum, 0)
	atomic.StoreInt64(&latencyCount, 0)
	atomic.StoreInt64(&ttfbSum, 0)
	atomic.StoreInt64(&ttfbCount, 0)
	atomic.StoreInt64(&meteringEnabled, 0)
	atomic.StoreInt64(&transportConns, 0)
	atomic.StoreInt64(&transportDialErr, 0)
	atomic.StoreInt64(&transportDNSErr, 0)
	atomic.StoreInt64(&transportClosed, 0)
	atomic.StoreInt64(&compressedStreams, 0)
	atomic.StoreInt64(&downstreamWriteErr, 0)
	atomic.StoreInt64(&streamFlushes, 0)
	atomic.StoreInt64(&requestSampleBytes, 0)
	atomic.StoreInt64(&responseSampleBytes, 0)
	for _, name := range reportNames {
		stats := reportQueryMetrics[name]
		atomic.StoreInt64(&stats.queries, 0)
		atomic.StoreInt64(&stats.errors, 0)
		atomic.StoreInt64(&stats.durationSumMS, 0)
		atomic.StoreInt64(&stats.durationCount, 0)
	}
}

func TestPrometheusMetricsIncludeLatencyTTFBAndKillSwitchState(t *testing.T) {
	resetForTest()
	SetMeteringEnabled(false)
	ObserveRequest(120, 35)

	var b strings.Builder
	writePrometheus(&b)
	out := b.String()

	for _, want := range []string{
		"request_latency_ms_sum 120",
		"request_latency_ms_count 1",
		"request_ttfb_ms_sum 35",
		"request_ttfb_ms_count 1",
		"metering_enabled 0",
		"capture_disabled 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, out)
		}
	}
}

func TestPrometheusMetricsIncludeTransportCounters(t *testing.T) {
	resetForTest()
	AddTransportConns(3)
	AddTransportDialErrs(2)
	AddTransportDNSErrs(1)
	AddTransportClosed(4)

	var b strings.Builder
	writePrometheus(&b)
	out := b.String()

	for _, want := range []string{
		"metering_proxy_transport_conns_created_total 3",
		"metering_proxy_transport_conns_closed_total 4",
		"metering_proxy_transport_dial_errors_total 2",
		"metering_proxy_transport_dns_errors_total 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, out)
		}
	}
}

func TestReportQueryMetricsSuccessAndErrorSemantics(t *testing.T) {
	resetForTest()

	ObserveReportQuery(ReportKeys, 12, nil)
	ObserveReportQuery(ReportKeys, 8, nil)
	ObserveReportQuery(ReportKeys, 5, errors.New("db unavailable"))

	queries, errs, sum, count, ok := SnapshotReportQuery(ReportKeys)
	if !ok {
		t.Fatal("keys report missing from fixed enum")
	}
	if queries != 3 || errs != 1 || sum != 25 || count != 3 {
		t.Fatalf("keys stats = queries=%d errors=%d sum=%d count=%d, want 3/1/25/3", queries, errs, sum, count)
	}

	q2, e2, s2, c2, ok := SnapshotReportQuery(ReportModels)
	if !ok || q2 != 0 || e2 != 0 || s2 != 0 || c2 != 0 {
		t.Fatalf("models stats leaked: %d %d %d %d ok=%v", q2, e2, s2, c2, ok)
	}
}

func TestReportQueryMetricsRejectHighCardinalityLabels(t *testing.T) {
	resetForTest()
	ObserveReportQuery(ReportKeys, 1, nil)

	banned := []ReportName{
		ReportName("key_hash"),
		ReportName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		ReportName("gpt-4o"),
		ReportName("/api/keys?key=abc"),
		ReportName("friend-a"),
		ReportName("req_123"),
	}
	for _, name := range banned {
		if IsKnownReport(name) {
			t.Fatalf("banned label %q unexpectedly known", name)
		}
		ObserveReportQuery(name, 99, errors.New("should be ignored"))
		if _, _, _, _, ok := SnapshotReportQuery(name); ok {
			t.Fatalf("banned label %q became representable", name)
		}
	}

	var b strings.Builder
	writePrometheus(&b)
	out := b.String()
	for _, bad := range []string{
		`report="key_hash"`,
		`report="gpt-4o"`,
		`report="/api/keys?key=abc"`,
		`report="friend-a"`,
		`report="req_123"`,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		if strings.Contains(out, bad) {
			t.Fatalf("exposition contains banned label material %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, `metering_proxy_report_queries_total{report="keys"} 1`) {
		t.Fatalf("missing fixed keys series:\n%s", out)
	}
}

func TestReportQueryMetricsExpositionEmitsFixedEnum(t *testing.T) {
	resetForTest()
	ObserveReportQuery(ReportOverview, 3, nil)
	ObserveReportQuery(ReportOverview, 2, errors.New("boom"))

	var b strings.Builder
	writePrometheus(&b)
	out := b.String()

	names := ReportNames()
	if len(names) == 0 {
		t.Fatal("empty report enum")
	}
	for _, name := range names {
		for _, metric := range []string{
			"metering_proxy_report_queries_total",
			"metering_proxy_report_query_errors_total",
			"metering_proxy_report_query_duration_ms_sum",
			"metering_proxy_report_query_duration_ms_count",
		} {
			wantPrefix := fmt.Sprintf(`%s{report="%s"}`, metric, name)
			if !strings.Contains(out, wantPrefix) {
				t.Fatalf("missing series %s in exposition", wantPrefix)
			}
		}
	}
	for _, want := range []string{
		`metering_proxy_report_queries_total{report="overview"} 2`,
		`metering_proxy_report_query_errors_total{report="overview"} 1`,
		`metering_proxy_report_query_duration_ms_sum{report="overview"} 5`,
		`metering_proxy_report_query_duration_ms_count{report="overview"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestReportQueryMetricsConcurrentSafe(t *testing.T) {
	resetForTest()
	const workers = 32
	const perWorker = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				var err error
				if (i+j)%5 == 0 {
					err = errors.New("x")
				}
				ObserveReportQuery(ReportTimeseries, 1, err)
			}
		}(i)
	}
	wg.Wait()

	queries, errs, sum, count, ok := SnapshotReportQuery(ReportTimeseries)
	if !ok {
		t.Fatal("timeseries missing")
	}
	wantQueries := int64(workers * perWorker)
	if queries != wantQueries || count != wantQueries || sum != wantQueries {
		t.Fatalf("queries/count/sum = %d/%d/%d want %d", queries, count, sum, wantQueries)
	}
	wantErrs := wantQueries / 5
	if errs != wantErrs {
		t.Fatalf("errors = %d want %d", errs, wantErrs)
	}
}
