package report

import (
	"context"
	"fmt"
	"log"
	"sort"

	"ai-gateway-metering-proxy/internal/db"
)

func (s *Service) Errors(ctx context.Context, filter ErrorsFilter) (ErrorsReport, error) {
	if s == nil {
		return ErrorsReport{}, fmt.Errorf("report service is not configured")
	}

	statuses := ErrorsSourceStatuses{
		HealthMetrics: SourceEmpty,
		RequestUsage:  SourceEmpty,
		LatestHealth:  SourceEmpty,
	}
	partial := false

	var sourceNames []string
	var timelines [][]ErrorTimelinePoint

	healthRows, healthErr := s.errors.ErrorTimelineReport(ctx, filter.Since)
	if healthErr != nil {
		if isContextError(healthErr) {
			return ErrorsReport{}, healthErr
		}
		log.Printf("errors report: health_metrics source unavailable: %v", healthErr)
		statuses.HealthMetrics = SourceUnavailable
		partial = true
	} else if len(healthRows) > 0 {
		statuses.HealthMetrics = SourceComplete
		sourceNames = append(sourceNames, "health_metrics")
		timelines = append(timelines, errorTimelineFromRows(healthRows))
	}

	reqRows, reqErr := s.errors.ErrorTimelineFromRequestsReport(ctx, filter.Since)
	if reqErr != nil {
		if isContextError(reqErr) {
			return ErrorsReport{}, reqErr
		}
		log.Printf("errors report: request_usage source unavailable: %v", reqErr)
		statuses.RequestUsage = SourceUnavailable
		partial = true
	} else if len(reqRows) > 0 {
		statuses.RequestUsage = SourceComplete
		sourceNames = append(sourceNames, "request_usage")
		timelines = append(timelines, errorTimelineFromRows(reqRows))
	}

	// Both core timeline sources failed: cannot produce a meaningful report.
	if statuses.HealthMetrics == SourceUnavailable && statuses.RequestUsage == SourceUnavailable {
		return ErrorsReport{}, fmt.Errorf("errors report sources unavailable")
	}

	timeline := combineErrorTimelines(timelines...)
	bucketCount := len(timeline)
	nonzero := filterNonzeroErrors(timeline)
	nonzeroCount := len(nonzero)
	if filter.Nonzero {
		timeline = nonzero
	}
	if timeline == nil {
		timeline = []ErrorTimelinePoint{}
	}

	source := "request_usage"
	if len(sourceNames) > 0 {
		source = sourceNames[0]
		for i := 1; i < len(sourceNames); i++ {
			source += "+" + sourceNames[i]
		}
	}

	out := ErrorsReport{
		Timeline:           timeline,
		Source:             source,
		BucketCount:        bucketCount,
		NonzeroBucketCount: nonzeroCount,
		Partial:            partial,
		SourceStatuses:     statuses,
	}

	latest, latestErr := s.errors.LatestHealthReport(ctx)
	if latestErr != nil {
		if isContextError(latestErr) {
			return ErrorsReport{}, latestErr
		}
		log.Printf("errors report: latest_health source unavailable: %v", latestErr)
		out.SourceStatuses.LatestHealth = SourceUnavailable
		out.Partial = true
	} else if latest != nil {
		out.SourceStatuses.LatestHealth = SourceComplete
		out.QueueDepth = latest.QueueDepth
		out.ParseErrors = latest.ParseErrors
		out.DBErrors = latest.DBErrors
		out.DroppedEvents = latest.DroppedEvents
	}

	return out, nil
}

func errorTimelineFromRows(rows []db.ErrorTimelineRow) []ErrorTimelinePoint {
	out := make([]ErrorTimelinePoint, len(rows))
	for i, row := range rows {
		out[i] = ErrorTimelinePoint{
			Timestamp:       row.Timestamp,
			Count:           row.Count,
			ParseErrors:     row.ParseErrors,
			DBErrors:        row.DBErrors,
			DroppedEvents:   row.DroppedEvents,
			BaselineMissing: row.BaselineMissing,
		}
	}
	return out
}

func filterNonzeroErrors(rows []ErrorTimelinePoint) []ErrorTimelinePoint {
	if len(rows) == 0 {
		return []ErrorTimelinePoint{}
	}
	filtered := make([]ErrorTimelinePoint, 0, len(rows))
	for _, r := range rows {
		if r.Count+r.ParseErrors+r.DBErrors+r.DroppedEvents > 0 {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func combineErrorTimelines(groups ...[]ErrorTimelinePoint) []ErrorTimelinePoint {
	byTimestamp := map[string]*ErrorTimelinePoint{}
	for _, rows := range groups {
		for _, row := range rows {
			key := row.Timestamp
			if key == "" {
				key = "unknown"
			}
			existing := byTimestamp[key]
			if existing == nil {
				copyRow := row
				byTimestamp[key] = &copyRow
				continue
			}
			existing.Count += row.Count
			existing.ParseErrors += row.ParseErrors
			existing.DBErrors += row.DBErrors
			existing.DroppedEvents += row.DroppedEvents
			// baseline_missing is sticky if any contributing bucket lacked a prior baseline.
			if row.BaselineMissing {
				existing.BaselineMissing = true
			}
		}
	}
	combined := make([]ErrorTimelinePoint, 0, len(byTimestamp))
	for _, row := range byTimestamp {
		combined = append(combined, *row)
	}
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Timestamp < combined[j].Timestamp
	})
	return combined
}
