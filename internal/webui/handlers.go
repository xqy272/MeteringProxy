package webui

import (
	"net/http"
	"strconv"
	"time"

	"ai-gateway-metering-proxy/internal/event"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	since, rangeKey := parseRange(r)
	dbOverview := s.db.Overview(since)
	dbOverview.Range = rangeKey

	// Compute cost
	var knownCost float64
	var unpricedModels int64
	models, modelsErr := s.db.Models(since)
	if modelsErr != nil {
		dbOverview.Cost.Error = modelsErr.Error()
	} else {
		imageModels, imageModelsErr := s.db.ImageModels(since)
		imageModelSet := map[string]struct{}{}
		for _, row := range imageModels {
			if row.Model != "" {
				imageModelSet[row.Model] = struct{}{}
			}
		}
		for _, m := range models {
			cost, costKnown := s.pricing.CostWithCacheCreation(m.Model, m.InputTokens, m.OutputTokens, m.ReasoningTokens, m.CachedTokens, m.CacheCreationTokens)
			if costKnown {
				knownCost += cost
			} else {
				if _, handledAsImage := imageModelSet[m.Model]; handledAsImage {
					continue
				}
				unpricedModels++
			}
		}
		if imageModelsErr == nil {
			imageCost, imageCostKnown, imageUnpriced := s.imageModelsCost(imageModels)
			knownCost += imageCost
			if !imageCostKnown {
				unpricedModels += imageUnpriced
			}
		} else if dbOverview.Cost.Error == "" {
			dbOverview.Cost.Error = imageModelsErr.Error()
		}
		partial := unpricedModels > 0

		// Update cost section
		costData := map[string]interface{}{
			"known_cost":      knownCost,
			"unpriced_models": unpricedModels,
			"partial":         partial,
		}
		if c, ok := dbOverview.Cost.Data.(map[string]interface{}); ok {
			for k, v := range costData {
				c[k] = v
			}
			dbOverview.Cost.Data = c
		} else {
			dbOverview.Cost.Data = costData
		}
	}

	// Update selected section with cost and failure_rate
	if sel, ok := dbOverview.Selected.Data.(map[string]interface{}); ok {
		sel["total_cost"] = knownCost
		if tr, ok2 := sel["total_requests"].(int64); ok2 && tr > 0 {
			if fr, ok3 := sel["failed_requests"].(int64); ok3 {
				sel["failure_rate"] = float64(fr) / float64(tr)
			}
		}
	}

	// Capture section from writer snapshot + DB capture stats
	qd, dropped, parseErrors, dbErrors := s.writer.Snapshot()
	capFailed, capSkipped, capErr := s.db.OverviewCaptureStats(since)
	captureData := map[string]interface{}{
		"queue_depth":     qd,
		"dropped_events":  dropped,
		"parse_errors":    parseErrors,
		"db_write_errors": dbErrors,
		"capture_failed":  capFailed,
		"capture_skipped": capSkipped,
	}
	dbOverview.Capture.Data = captureData
	if capErr != nil {
		dbOverview.Capture.Error = capErr.Error()
	} else if dbOverview.Capture.Error == "" {
		if dropped > 0 || parseErrors > 0 || dbErrors > 0 || qd > 0 || capFailed > 0 || capSkipped > 0 {
			captureData["status"] = "attention"
		} else {
			captureData["status"] = "healthy"
		}
	}

	report := event.OverviewReport{
		Range:    dbOverview.Range,
		Selected: event.OverviewSection{Data: dbOverview.Selected.Data, Error: dbOverview.Selected.Error},
		Recent1h: event.OverviewSection{Data: dbOverview.Recent1h.Data, Error: dbOverview.Recent1h.Error},
		Capture:  event.OverviewSection{Data: dbOverview.Capture.Data, Error: dbOverview.Capture.Error},
		Cost:     event.OverviewSection{Data: dbOverview.Cost.Data, Error: dbOverview.Cost.Error},
	}
	writeJSON(w, report)
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
	since, rangeKey := parseRange(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	dbIssues, err := s.db.Issues(since, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	items := make([]event.IssueReport, len(dbIssues))
	for i, row := range dbIssues {
		items[i] = event.IssueReport{
			Class:       row.Class,
			Label:       row.Label,
			Count:       row.Count,
			Severity:    row.Severity,
			SourceGroup: row.SourceGroup,
			LatestAt:    row.LatestAt,
			Status:      row.Status,
			Endpoint:    row.Endpoint,
			Model:       row.Model,
			ModelSource: row.ModelSource,
			APIKeyHash:  row.APIKeyHash,
			ErrorType:   row.ErrorType,
			ErrorCode:   row.ErrorCode,
			Message:     row.Message,
			RequestID:   row.RequestID,
		}
	}

	parseErrors, dbErrors, dropped, scope := s.systemIssueCounts(since)

	systemItems := []event.IssueSystemItem{}
	if parseErrors > 0 {
		systemItems = append(systemItems, event.IssueSystemItem{
			Class: "capture_parse_error", Label: "Capture parse error",
			Count: parseErrors, Scope: scope, Severity: "warning",
		})
	}
	if dropped > 0 {
		systemItems = append(systemItems, event.IssueSystemItem{
			Class: "dropped_event", Label: "Dropped event",
			Count: dropped, Scope: scope, Severity: "warning",
		})
	}
	if dbErrors > 0 {
		systemItems = append(systemItems, event.IssueSystemItem{
			Class: "db_write_error", Label: "DB write error",
			Count: dbErrors, Scope: scope, Severity: "error",
		})
	}
	if s.usageQueuePoller != nil {
		connected, _, _ := s.usageQueuePoller.Snapshot()
		if !connected {
			systemItems = append(systemItems, event.IssueSystemItem{
				Class: "side_channel_disconnected", Label: "Side-channel disconnected",
				Count: 1, Scope: "side_channel", Severity: "warning",
			})
		}
	}

	resp := event.IssuesResponse{
		Range: rangeKey,
		Total: len(items),
		Items: items,
		System: event.IssuesSystem{
			ParseErrors:   parseErrors,
			DBErrors:      dbErrors,
			DroppedEvents: dropped,
			Items:         systemItems,
		},
	}
	writeJSON(w, resp)
}

func (s *Server) systemIssueCounts(since time.Time) (parseErrors, dbErrors, dropped int64, scope string) {
	rows, err := s.db.ErrorTimeline(since)
	if err == nil && len(rows) > 0 {
		for _, row := range rows {
			parseErrors += row.ParseErrors
			dbErrors += row.DBErrors
			dropped += row.DroppedEvents
		}
		return parseErrors, dbErrors, dropped, "range"
	}
	_, dropped, parseErrors, dbErrors = s.writer.Snapshot()
	return parseErrors, dbErrors, dropped, "process"
}
