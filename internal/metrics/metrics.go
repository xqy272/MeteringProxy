package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

// Counters shared across the process. The writer and proxy update these
// via the exported Set* functions so that /metrics reflects live state.
var (
	sseLineSkips      int64
	queueDepth        int64
	droppedEvents     int64
	parseErrors       int64
	dbWriteErrors     int64
	latencySum        int64
	latencyCount      int64
	ttfbSum           int64
	ttfbCount         int64
	meteringEnabled   int64
	transportConns    int64
	transportDialErr  int64
	transportDNSErr   int64
	transportClosed   int64
)

func SetQueueDepth(v int64)    { atomic.StoreInt64(&queueDepth, v) }
func AddDroppedEvents(n int64) { atomic.AddInt64(&droppedEvents, n) }
func AddParseErrors(n int64)   { atomic.AddInt64(&parseErrors, n) }
func AddDBWriteErrors(n int64) { atomic.AddInt64(&dbWriteErrors, n) }
func AddSSELineSkips(n int64)  { atomic.AddInt64(&sseLineSkips, n) }
func SSELineSkips() int64      { return atomic.LoadInt64(&sseLineSkips) }
func SetMeteringEnabled(enabled bool) {
	if enabled {
		atomic.StoreInt64(&meteringEnabled, 1)
		return
	}
	atomic.StoreInt64(&meteringEnabled, 0)
}
func ObserveRequest(latencyMs, ttfbMs int64) {
	atomic.AddInt64(&latencySum, latencyMs)
	atomic.AddInt64(&latencyCount, 1)
	atomic.AddInt64(&ttfbSum, ttfbMs)
	atomic.AddInt64(&ttfbCount, 1)
}

// Transport counters are updated only when a connection is created or fails
// to dial — never on the per-request hot path — so they use lock-free atomics.
func AddTransportConns(n int64)   { atomic.AddInt64(&transportConns, n) }
func AddTransportDialErrs(n int64) { atomic.AddInt64(&transportDialErr, n) }
func AddTransportDNSErrs(n int64)  { atomic.AddInt64(&transportDNSErr, n) }
func AddTransportClosed(n int64)   { atomic.AddInt64(&transportClosed, n) }

// Handler returns an HTTP handler that serves Prometheus text metrics.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		writePrometheus(w)
	})
}

func writePrometheus(w io.Writer) {
	qd := atomic.LoadInt64(&queueDepth)
	de := atomic.LoadInt64(&droppedEvents)
	pe := atomic.LoadInt64(&parseErrors)
	dbe := atomic.LoadInt64(&dbWriteErrors)
	sse := atomic.LoadInt64(&sseLineSkips)
	metering := atomic.LoadInt64(&meteringEnabled)

	sum := atomic.LoadInt64(&latencySum)
	cnt := atomic.LoadInt64(&latencyCount)
	ttfb := atomic.LoadInt64(&ttfbSum)
	ttfbCnt := atomic.LoadInt64(&ttfbCount)

	fmt.Fprintf(w, "# HELP queue_depth Current number of events in the write queue\n")
	fmt.Fprintf(w, "# TYPE queue_depth gauge\n")
	fmt.Fprintf(w, "queue_depth %d\n", qd)

	fmt.Fprintf(w, "# HELP dropped_events_total Total number of events dropped due to queue overflow\n")
	fmt.Fprintf(w, "# TYPE dropped_events_total counter\n")
	fmt.Fprintf(w, "dropped_events_total %d\n", de)

	fmt.Fprintf(w, "# HELP parse_errors_total Total number of usage parse errors\n")
	fmt.Fprintf(w, "# TYPE parse_errors_total counter\n")
	fmt.Fprintf(w, "parse_errors_total %d\n", pe)

	fmt.Fprintf(w, "# HELP db_write_errors_total Total number of database write errors\n")
	fmt.Fprintf(w, "# TYPE db_write_errors_total counter\n")
	fmt.Fprintf(w, "db_write_errors_total %d\n", dbe)

	fmt.Fprintf(w, "# HELP sse_line_skips_total Total number of SSE lines skipped (too long)\n")
	fmt.Fprintf(w, "# TYPE sse_line_skips_total counter\n")
	fmt.Fprintf(w, "sse_line_skips_total %d\n", sse)

	fmt.Fprintf(w, "# HELP request_latency_ms Request latency in milliseconds\n")
	fmt.Fprintf(w, "# TYPE request_latency_ms summary\n")
	fmt.Fprintf(w, "request_latency_ms_sum %d\n", sum)
	fmt.Fprintf(w, "request_latency_ms_count %d\n", cnt)

	fmt.Fprintf(w, "# HELP request_ttfb_ms Time to first byte in milliseconds\n")
	fmt.Fprintf(w, "# TYPE request_ttfb_ms summary\n")
	fmt.Fprintf(w, "request_ttfb_ms_sum %d\n", ttfb)
	fmt.Fprintf(w, "request_ttfb_ms_count %d\n", ttfbCnt)

	fmt.Fprintf(w, "# HELP metering_enabled Whether metering capture is enabled\n")
	fmt.Fprintf(w, "# TYPE metering_enabled gauge\n")
	fmt.Fprintf(w, "metering_enabled %d\n", metering)

	fmt.Fprintf(w, "# HELP capture_disabled Whether capture is disabled by kill switch\n")
	fmt.Fprintf(w, "# TYPE capture_disabled gauge\n")
	fmt.Fprintf(w, "capture_disabled %d\n", 1-metering)

	tConns := atomic.LoadInt64(&transportConns)
	tDial := atomic.LoadInt64(&transportDialErr)
	tDNS := atomic.LoadInt64(&transportDNSErr)
	tClosed := atomic.LoadInt64(&transportClosed)

	fmt.Fprintf(w, "# HELP metering_proxy_transport_conns_created_total Total upstream connections established by the proxy transport\n")
	fmt.Fprintf(w, "# TYPE metering_proxy_transport_conns_created_total counter\n")
	fmt.Fprintf(w, "metering_proxy_transport_conns_created_total %d\n", tConns)

	fmt.Fprintf(w, "# HELP metering_proxy_transport_conns_closed_total Total upstream connections closed by the proxy transport\n")
	fmt.Fprintf(w, "# TYPE metering_proxy_transport_conns_closed_total counter\n")
	fmt.Fprintf(w, "metering_proxy_transport_conns_closed_total %d\n", tClosed)

	fmt.Fprintf(w, "# HELP metering_proxy_transport_dial_errors_total Total upstream dial failures (connection refused, timeout, etc.)\n")
	fmt.Fprintf(w, "# TYPE metering_proxy_transport_dial_errors_total counter\n")
	fmt.Fprintf(w, "metering_proxy_transport_dial_errors_total %d\n", tDial)

	fmt.Fprintf(w, "# HELP metering_proxy_transport_dns_errors_total Total upstream DNS resolution failures\n")
	fmt.Fprintf(w, "# TYPE metering_proxy_transport_dns_errors_total counter\n")
	fmt.Fprintf(w, "metering_proxy_transport_dns_errors_total %d\n", tDNS)
}
