package report

import (
	"time"

	"ai-gateway-metering-proxy/internal/metrics"
)

// observeReport records exactly one metrics sample for a Service method call.
// It preserves the original result/error and never labels by filter values.
func observeReport[T any](name metrics.ReportName, fn func() (T, error)) (T, error) {
	start := time.Now()
	out, err := fn()
	metrics.ObserveReportQuery(name, time.Since(start), err)
	return out, err
}
