package report

import (
	"context"
	"fmt"
	"log"

	"ai-gateway-metering-proxy/internal/metrics"
)

func (s *Service) Health(ctx context.Context, filter HealthFilter) (HealthDashboardReport, error) {
	return observeReport(metrics.ReportHealth, func() (HealthDashboardReport, error) {
		if s == nil {
			return HealthDashboardReport{}, fmt.Errorf("report service is not configured")
		}

		out := HealthDashboardReport{
			MeteringEnabled: filter.MeteringEnabled,
			CaptureDisabled: !filter.MeteringEnabled,
			SourceStatuses: HealthSourceStatuses{
				Runtime:      SourceComplete,
				LatestHealth: SourceEmpty,
			},
		}

		if s.capture != nil {
			qd, dropped, parseErrors, dbErrors := s.capture.Snapshot()
			out.QueueDepth = qd
			out.DroppedEvents = dropped
			out.ParseErrors = parseErrors
			out.DBWriteErrors = dbErrors
		}

		latest, err := s.errors.LatestHealthReport(ctx)
		if err != nil {
			if isContextError(err) {
				return HealthDashboardReport{}, err
			}
			log.Printf("health report: latest_health source unavailable: %v", err)
			out.SourceStatuses.LatestHealth = SourceUnavailable
			out.Partial = true
			return out, nil
		}
		if latest != nil {
			out.SourceStatuses.LatestHealth = SourceComplete
			out.LatestHealth = HealthSnapshot{
				Timestamp:     latest.Timestamp,
				QueueDepth:    latest.QueueDepth,
				DroppedEvents: latest.DroppedEvents,
				ParseErrors:   latest.ParseErrors,
				DBErrors:      latest.DBErrors,
				SSELineSkips:  latest.SSELineSkips,
			}
		}
		return out, nil
	})
}
