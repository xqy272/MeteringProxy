package db

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

type OverviewSelectedRow struct {
	TotalRequests        int64
	FailedRequests       int64
	TotalInputTokens     int64
	TotalOutputTokens    int64
	TotalReasoningTokens int64
	TotalCachedTokens    int64
	TotalTokens          int64
	P95LatencyMs         int64
	P95TTFBMs            int64
}

type OverviewLatestErrorRow struct {
	LatestAt    string
	Status      int
	Endpoint    string
	Model       string
	ModelSource string
	Class       string
	Message     string
	RequestID   string
}

type OverviewRecentRow struct {
	TotalRequests  int64
	FailedRequests int64
	P95LatencyMs   int64
	LatestError    *OverviewLatestErrorRow
}

type OverviewReportData struct {
	Selected         OverviewSelectedRow
	Recent           OverviewRecentRow
	CaptureFailed    int64
	CaptureSkipped   int64
	TextCostBuckets  []TextCostBucketRow
	ImageCostBuckets []ImageCostBucketRow
}

// OverviewReportSnapshot loads every database-backed overview section and its
// cost buckets from one read transaction. Any query failure is atomic; callers
// never receive a zero-filled section that looks complete.
func (db *DB) OverviewReportSnapshot(ctx context.Context, since, recentSince time.Time) (*OverviewReportData, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	selected, err := queryOverviewSelected(ctx, tx, since)
	if err != nil {
		return nil, err
	}
	recent, err := queryOverviewRecent(ctx, tx, recentSince)
	if err != nil {
		return nil, err
	}
	captureFailed, captureSkipped, err := queryOverviewCapture(ctx, tx, since)
	if err != nil {
		return nil, err
	}
	textCost, imageCost, err := queryCostBuckets(ctx, tx, CostBucketFilter{Since: since})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &OverviewReportData{
		Selected: selected, Recent: recent,
		CaptureFailed: captureFailed, CaptureSkipped: captureSkipped,
		TextCostBuckets: textCost, ImageCostBuckets: imageCost,
	}, nil
}

func queryOverviewSelected(ctx context.Context, q reportQueryContext, since time.Time) (OverviewSelectedRow, error) {
	var row OverviewSelectedRow
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(total_tokens), 0)
		FROM request_usage WHERE created_at_unix >= ?
	`, since.Unix()).Scan(
		&row.TotalRequests, &row.FailedRequests,
		&row.TotalInputTokens, &row.TotalOutputTokens, &row.TotalReasoningTokens,
		&row.TotalCachedTokens, &row.TotalTokens,
	)
	if err != nil {
		return OverviewSelectedRow{}, err
	}
	row.P95LatencyMs, err = queryPercentileInt(ctx, q, since, "latency_ms", 0.95, activitySampleLimit)
	if err != nil {
		return OverviewSelectedRow{}, fmt.Errorf("overview selected latency percentile: %w", err)
	}
	row.P95TTFBMs, err = queryPercentileInt(ctx, q, since, "ttfb_ms", 0.95, activitySampleLimit)
	if err != nil {
		return OverviewSelectedRow{}, fmt.Errorf("overview selected ttfb percentile: %w", err)
	}
	return row, nil
}

func queryOverviewRecent(ctx context.Context, q reportQueryContext, since time.Time) (OverviewRecentRow, error) {
	var row OverviewRecentRow
	if err := q.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(CASE WHEN status >= 400 THEN 1 END)
		FROM request_usage WHERE created_at_unix >= ?
	`, since.Unix()).Scan(&row.TotalRequests, &row.FailedRequests); err != nil {
		return OverviewRecentRow{}, err
	}
	var err error
	row.P95LatencyMs, err = queryPercentileInt(ctx, q, since, "latency_ms", 0.95, activitySampleLimit)
	if err != nil {
		return OverviewRecentRow{}, fmt.Errorf("overview recent latency percentile: %w", err)
	}

	latest := &OverviewLatestErrorRow{}
	err = q.QueryRowContext(ctx, `
		SELECT
			COALESCE(created_at, ''),
			COALESCE(status, 0),
			COALESCE(endpoint, ''),
			`+effectiveModelExpr+`,
			`+modelSourceExpr+`,
			COALESCE(NULLIF(TRIM(error_class), ''), 'unknown'),
			COALESCE(NULLIF(TRIM(error_code), ''), NULLIF(TRIM(error_type), ''), NULLIF(TRIM(error_class), ''), error, ''),
			COALESCE(request_id, '')
		FROM request_usage
		WHERE created_at_unix >= ? AND status >= 400
		ORDER BY created_at_unix DESC, id DESC
		LIMIT 1
	`, since.Unix()).Scan(
		&latest.LatestAt, &latest.Status, &latest.Endpoint, &latest.Model,
		&latest.ModelSource, &latest.Class, &latest.Message, &latest.RequestID,
	)
	switch err {
	case nil:
		row.LatestError = latest
	case sql.ErrNoRows:
	default:
		return OverviewRecentRow{}, err
	}
	return row, nil
}

func queryOverviewCapture(ctx context.Context, q reportQueryContext, since time.Time) (failed, skipped int64, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT
			COUNT(CASE WHEN capture_outcome = 'failed' THEN 1 END),
			COUNT(CASE WHEN capture_outcome = 'skipped' THEN 1 END)
		FROM request_usage
		WHERE created_at_unix >= ?
	`, since.Unix()).Scan(&failed, &skipped)
	return failed, skipped, err
}

func queryPercentileInt(ctx context.Context, q reportQueryContext, since time.Time, column string, percentile float64, limit int) (int64, error) {
	switch column {
	case "latency_ms", "ttfb_ms":
	default:
		return 0, fmt.Errorf("unsupported percentile column %q", column)
	}
	if limit <= 0 {
		limit = activitySampleLimit
	}
	var count int64
	if err := q.QueryRowContext(ctx, `
		WITH sampled AS (
			SELECT `+column+`
			FROM request_usage
			WHERE created_at_unix >= ?
			ORDER BY id DESC
			LIMIT ?
		)
		SELECT COUNT(*) FROM sampled WHERE `+column+` > 0
	`, since.Unix(), limit).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	offset := int64(math.Ceil(percentile*float64(count))) - 1
	if offset < 0 {
		offset = 0
	}
	if offset >= count {
		offset = count - 1
	}
	var value int64
	err := q.QueryRowContext(ctx, `
		WITH sampled AS (
			SELECT `+column+`
			FROM request_usage
			WHERE created_at_unix >= ?
			ORDER BY id DESC
			LIMIT ?
		)
		SELECT `+column+`
		FROM sampled
		WHERE `+column+` > 0
		ORDER BY `+column+` ASC
		LIMIT 1 OFFSET ?
	`, since.Unix(), limit, offset).Scan(&value)
	return value, err
}
