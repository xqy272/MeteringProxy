package metrics

import (
	"strings"
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
