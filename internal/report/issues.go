package report

import (
	"ai-gateway-metering-proxy/internal/metrics"
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

func (s *Service) Issues(ctx context.Context, filter IssueFilter) (IssuesReport, error) {
	return observeReport(metrics.ReportIssues, func() (IssuesReport, error) {
		if s == nil {
			return IssuesReport{}, fmt.Errorf("report service is not configured")
		}
		limit := filter.Limit
		if limit <= 0 || limit > 100 {
			limit = 20
		}
		includeGlobal := filter.KeyHash == ""

		data, err := s.issues.IssuesReport(ctx, db.IssueFilter{
			Scope:         db.ReportScope{Since: filter.Since, KeyHash: filter.KeyHash},
			Limit:         limit,
			IncludeGlobal: includeGlobal,
		})
		if err != nil {
			return IssuesReport{}, err
		}
		sources := IssuesSourceStatuses{
			RequestUsage:     IssueSourceComplete,
			SideChannel:      IssueSourceNotApplicable,
			CredentialHealth: IssueSourceNotApplicable,
			Quota:            IssueSourceNotApplicable,
			System:           IssueSourceNotApplicable,
		}
		items := issueReportsFromRows(data.RequestUsage)
		partial := false

		if includeGlobal {
			if err := data.SideChannelErr; err != nil {
				if isContextError(err) {
					return IssuesReport{}, err
				}
				log.Printf("issues report: side_channel source unavailable: %v", err)
				sources.SideChannel = IssueSourceUnavailable
				partial = true
			} else {
				sources.SideChannel = IssueSourceComplete
				items = append(items, issueReportsFromRows(data.SideChannel)...)
			}

			if err := data.CredentialErr; err != nil {
				if isContextError(err) {
					return IssuesReport{}, err
				}
				log.Printf("issues report: credential_health source unavailable: %v", err)
				sources.CredentialHealth = IssueSourceUnavailable
				partial = true
			} else {
				sources.CredentialHealth = IssueSourceComplete
				items = append(items, issueReportsFromRows(data.Credential)...)
			}

			if err := data.QuotaErr; err != nil {
				if isContextError(err) {
					return IssuesReport{}, err
				}
				// Quota current and/or refresh failed: never mark complete.
				// Successful subset rows (if any) remain visible under partial.
				log.Printf("issues report: quota source unavailable: %v", err)
				sources.Quota = IssueSourceUnavailable
				partial = true
				items = append(items, issueReportsFromRows(data.Quota)...)
			} else {
				sources.Quota = IssueSourceComplete
				items = append(items, issueReportsFromRows(data.Quota)...)
			}

			system, systemStatus, systemPartial, err := s.buildSystemIssues(ctx, filter.Since)
			if err != nil {
				return IssuesReport{}, err
			}
			sources.System = systemStatus
			if systemPartial {
				partial = true
			}
			sortIssueReports(items)
			if len(items) > limit {
				items = items[:limit]
			}
			if items == nil {
				items = []IssueReport{}
			}
			if system.Items == nil {
				system.Items = []IssueSystemItem{}
			}
			return IssuesReport{
				Range:   filter.Range,
				Total:   len(items),
				Items:   items,
				System:  system,
				Partial: partial,
				Sources: sources,
			}, nil
		}

		// Key-scoped: request_usage only; no global/system sources.
		sortIssueReports(items)
		if len(items) > limit {
			items = items[:limit]
		}
		if items == nil {
			items = []IssueReport{}
		}
		return IssuesReport{
			Range:   filter.Range,
			Total:   len(items),
			Items:   items,
			System:  IssuesSystem{Items: []IssueSystemItem{}},
			Sources: sources,
		}, nil
	})
}

func (s *Service) buildSystemIssues(ctx context.Context, since time.Time) (IssuesSystem, string, bool, error) {
	system := IssuesSystem{Items: []IssueSystemItem{}}
	parseErrors, dbErrors, dropped, scope, status, degraded, err := s.systemIssueCounts(ctx, since)
	if err != nil {
		return IssuesSystem{}, "", false, err
	}

	system.ParseErrors = parseErrors
	system.DBErrors = dbErrors
	system.DroppedEvents = dropped

	if parseErrors > 0 {
		system.Items = append(system.Items, IssueSystemItem{
			Class: "capture_parse_error", Label: "Capture parse error",
			Count: parseErrors, Scope: scope, Severity: "warning",
		})
	}
	if dropped > 0 {
		system.Items = append(system.Items, IssueSystemItem{
			Class: "dropped_event", Label: "Dropped event",
			Count: dropped, Scope: scope, Severity: "warning",
		})
	}
	if dbErrors > 0 {
		system.Items = append(system.Items, IssueSystemItem{
			Class: "db_write_error", Label: "DB write error",
			Count: dbErrors, Scope: scope, Severity: "error",
		})
	}
	if s.sideChannel != nil {
		connected, _, _ := s.sideChannel.Snapshot()
		if !connected {
			system.Items = append(system.Items, IssueSystemItem{
				Class: "side_channel_disconnected", Label: "Side-channel disconnected",
				Count: 1, Scope: "side_channel", Severity: "warning",
			})
		}
	}
	return system, status, degraded, nil
}

func (s *Service) systemIssueCounts(ctx context.Context, since time.Time) (parseErrors, dbErrors, dropped int64, scope, status string, degraded bool, err error) {
	rows, timelineErr := s.issues.ErrorTimelineReport(ctx, since)
	if timelineErr != nil {
		if isContextError(timelineErr) {
			// Cancellation/deadline must not degrade to process counters.
			return 0, 0, 0, "", "", false, timelineErr
		}
		// Non-context ErrorTimeline failure: explicit partial/unavailable with process fallback.
		log.Printf("issues report: system/error_timeline source unavailable: %v", timelineErr)
		if s.capture != nil {
			_, dropped, parseErrors, dbErrors = s.capture.Snapshot()
		}
		return parseErrors, dbErrors, dropped, "process", IssueSourceUnavailable, true, nil
	}
	if len(rows) > 0 {
		for _, row := range rows {
			parseErrors += row.ParseErrors
			dbErrors += row.DBErrors
			dropped += row.DroppedEvents
		}
		return parseErrors, dbErrors, dropped, "range", IssueSourceComplete, false, nil
	}
	// Empty timeline is legitimate: fall back to process counters without error.
	if s.capture != nil {
		_, dropped, parseErrors, dbErrors = s.capture.Snapshot()
	}
	return parseErrors, dbErrors, dropped, "process", IssueSourceComplete, false, nil
}

func isContextError(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

func issueReportsFromRows(rows []db.IssueRow) []IssueReport {
	if len(rows) == 0 {
		return nil
	}
	out := make([]IssueReport, len(rows))
	for i, row := range rows {
		out[i] = IssueReport{
			Class: row.Class, Label: row.Label, Count: row.Count, Severity: row.Severity,
			SourceGroup: row.SourceGroup, LatestAt: row.LatestAt, Status: row.Status,
			Endpoint: row.Endpoint, Model: row.Model, ModelSource: row.ModelSource,
			APIKeyHash: row.APIKeyHash, ErrorType: row.ErrorType, ErrorCode: row.ErrorCode,
			Message: row.Message, RequestID: row.RequestID,
		}
	}
	return out
}

func sortIssueReports(rows []IssueReport) {
	sort.SliceStable(rows, func(i, j int) bool {
		if issueSeverityRank(rows[i].Severity) != issueSeverityRank(rows[j].Severity) {
			return issueSeverityRank(rows[i].Severity) < issueSeverityRank(rows[j].Severity)
		}
		if rows[i].LatestAt != rows[j].LatestAt {
			return rows[i].LatestAt > rows[j].LatestAt
		}
		return rows[i].Count > rows[j].Count
	})
}

func issueSeverityRank(severity string) int {
	switch severity {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}
