package db

import "time"

// Overview returns a composite overview with selected range, recent 1h,
// capture health, and cost sections.
func (db *DB) Overview(since time.Time) *OverviewRow {
	row := &OverviewRow{Range: "24h"}

	var totalRequests, failedRequests, inputTokens, outputTokens, reasoningTokens, cachedTokens, totalTokens int64
	err := db.read.QueryRow(`
		SELECT COUNT(*), COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM request_usage WHERE created_at_unix >= ?
	`, since.Unix()).Scan(
		&totalRequests, &failedRequests,
		&inputTokens, &outputTokens, &reasoningTokens, &cachedTokens, &totalTokens,
	)
	if err == nil {
		p95Lat, _ := db.percentileInt(since, "latency_ms", 0.95, activitySampleLimit)
		p95TTFB, _ := db.percentileInt(since, "ttfb_ms", 0.95, activitySampleLimit)
		row.Selected.Data = map[string]interface{}{
			"total_requests":         totalRequests,
			"failed_requests":        failedRequests,
			"total_input_tokens":     inputTokens,
			"total_output_tokens":    outputTokens,
			"total_reasoning_tokens": reasoningTokens,
			"total_cached_tokens":    cachedTokens,
			"total_tokens":           totalTokens,
			"p95_latency_ms":         p95Lat,
			"p95_ttfb_ms":            p95TTFB,
		}
	} else {
		row.Selected.Error = err.Error()
	}

	// Recent 1h
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	var rTotal, rFailed int64
	err = db.read.QueryRow(`
		SELECT COUNT(*), COUNT(CASE WHEN status >= 400 THEN 1 END)
		FROM request_usage WHERE created_at_unix >= ?
	`, oneHourAgo.Unix()).Scan(&rTotal, &rFailed)
	if err == nil {
		p95Lat1h, _ := db.percentileInt(oneHourAgo, "latency_ms", 0.95, activitySampleLimit)
		failureRate := 0.0
		if rTotal > 0 {
			failureRate = float64(rFailed) / float64(rTotal)
		}
		recentData := map[string]interface{}{
			"total_requests":  rTotal,
			"failed_requests": rFailed,
			"failure_rate":    failureRate,
			"p95_latency_ms":  p95Lat1h,
			"latest_error":    nil,
		}
		var latest struct {
			CreatedAt   string
			Status      int
			Endpoint    string
			Model       string
			ModelSource string
			Class       string
			Message     string
			RequestID   string
		}
		latestErr := db.read.QueryRow(`
			SELECT
				COALESCE(created_at, ''),
				COALESCE(status, 0),
				COALESCE(endpoint, ''),
				`+effectiveModelExpr+`,
				`+modelSourceExpr+`,
				COALESCE(NULLIF(TRIM(error_class), ''), 'unknown'),
				COALESCE(NULLIF(TRIM(error_message), ''), error, ''),
				COALESCE(request_id, '')
			FROM request_usage
			WHERE created_at_unix >= ? AND status >= 400
			ORDER BY created_at_unix DESC, id DESC
			LIMIT 1
		`, oneHourAgo.Unix()).Scan(
			&latest.CreatedAt, &latest.Status, &latest.Endpoint, &latest.Model,
			&latest.ModelSource, &latest.Class, &latest.Message, &latest.RequestID,
		)
		if latestErr == nil {
			recentData["latest_error"] = map[string]interface{}{
				"latest_at":    latest.CreatedAt,
				"status":       latest.Status,
				"endpoint":     latest.Endpoint,
				"model":        latest.Model,
				"model_source": latest.ModelSource,
				"class":        latest.Class,
				"message":      latest.Message,
				"request_id":   latest.RequestID,
			}
		}
		row.Recent1h.Data = recentData
	} else {
		row.Recent1h.Error = err.Error()
	}

	row.Capture.Data = map[string]interface{}{"status": "healthy"}
	row.Cost.Data = map[string]interface{}{"partial": false}
	return row
}

// Issues returns aggregated request-level issues.
func (db *DB) Issues(since time.Time, limit int) []IssueRow {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := db.read.Query(
		`WITH base AS (
			SELECT
				id,
				COALESCE(created_at, '') AS created_at,
				COALESCE(created_at_unix, 0) AS created_at_unix,
				COALESCE(NULLIF(TRIM(error_class), ''), 'unknown') AS class,
				COALESCE(status, 0) AS status,
				COALESCE(endpoint, '') AS endpoint,
				`+effectiveModelExpr+` AS model,
				COALESCE(api_key_hash, '') AS api_key_hash,
				CASE WHEN NULLIF(TRIM(model_returned), '') IS NOT NULL THEN 1 ELSE 0 END AS returned_present,
				CASE WHEN NULLIF(TRIM(model_requested), '') IS NOT NULL THEN 1 ELSE 0 END AS requested_present,
				COALESCE(error_type, '') AS error_type,
				COALESCE(error_code, '') AS error_code,
				COALESCE(error_message, '') AS error_message,
				COALESCE(request_id, '') AS request_id
			FROM request_usage
			WHERE created_at_unix >= ? AND status >= 400
		),
		agg AS (
			SELECT
				class,
				status,
				endpoint,
				model,
				api_key_hash,
				COUNT(*) AS count,
				MAX(created_at_unix) AS latest_unix,
				CASE
					WHEN SUM(returned_present) > 0 THEN 'returned'
					WHEN SUM(requested_present) > 0 THEN 'requested'
					ELSE 'unidentified'
				END AS model_source
			FROM base
			GROUP BY class, status, endpoint, model, api_key_hash
		),
		latest AS (
			SELECT *,
				ROW_NUMBER() OVER (
					PARTITION BY class, status, endpoint, model, api_key_hash
					ORDER BY created_at_unix DESC, id DESC
				) AS rn
			FROM base
		)
		SELECT
			agg.class,
			agg.count,
			latest.created_at,
			agg.status,
			agg.endpoint,
			agg.model,
			agg.model_source,
			agg.api_key_hash,
			latest.error_type,
			latest.error_code,
			latest.error_message,
			latest.request_id
		FROM agg
		JOIN latest
			ON latest.rn = 1
			AND latest.class = agg.class
			AND latest.status = agg.status
			AND latest.endpoint = agg.endpoint
			AND latest.model = agg.model
			AND latest.api_key_hash = agg.api_key_hash
		ORDER BY
			CASE
				WHEN agg.class IN ('auth_failed', 'quota_exhausted', 'proxy_upstream_error', 'db_write_error') THEN 0
				WHEN agg.class IN ('rate_limited', 'upstream_5xx', 'context_length', 'capture_parse_error', 'dropped_event') THEN 1
				ELSE 2
			END ASC,
			agg.latest_unix DESC,
			agg.count DESC
		LIMIT ?`,
		since.Unix(), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []IssueRow
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.Class, &r.Count, &r.LatestAt, &r.Status, &r.Endpoint, &r.Model, &r.ModelSource, &r.APIKeyHash, &r.ErrorType, &r.ErrorCode, &r.Message, &r.RequestID); err != nil {
			continue
		}
		r.Label = classLabel(r.Class)
		r.Severity = classSeverity(r.Class)
		result = append(result, r)
	}
	return result
}

func classLabel(class string) string {
	switch class {
	case "quota_exhausted":
		return "Quota exhausted"
	case "rate_limited":
		return "Rate limited"
	case "auth_failed":
		return "Auth failed"
	case "invalid_request":
		return "Invalid request"
	case "context_length":
		return "Context length exceeded"
	case "upstream_5xx":
		return "Upstream 5xx error"
	case "proxy_upstream_error":
		return "Proxy upstream error"
	case "capture_parse_error":
		return "Capture parse error"
	case "db_write_error":
		return "DB write error"
	case "dropped_event":
		return "Dropped event"
	default:
		return "Unclassified issue"
	}
}

func classSeverity(class string) string {
	switch class {
	case "auth_failed", "quota_exhausted", "proxy_upstream_error", "db_write_error":
		return "error"
	case "rate_limited", "upstream_5xx", "context_length", "capture_parse_error", "dropped_event":
		return "warning"
	default:
		return "info"
	}
}

func (db *DB) OverviewCaptureStats(since time.Time, failed, skipped *int64) {
	if failed != nil {
		db.read.QueryRow(`
			SELECT COUNT(*) FROM request_usage
			WHERE created_at_unix >= ? AND capture_outcome = 'failed'
		`, since.Unix()).Scan(failed)
	}
	if skipped != nil {
		db.read.QueryRow(`
			SELECT COUNT(*) FROM request_usage
			WHERE created_at_unix >= ? AND capture_outcome = 'skipped'
		`, since.Unix()).Scan(skipped)
	}
}
