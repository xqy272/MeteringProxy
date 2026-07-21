package db

import (
	"context"
	"database/sql"
)

func (db *DB) ActivityReport(ctx context.Context, scope ReportScope) (*ActivityRow, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	row, err := queryActivity(ctx, tx, scope)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return row, nil
}

func queryActivity(ctx context.Context, q reportQueryContext, scope ReportScope) (*ActivityRow, error) {
	where, args := reportScopeWhere(scope)
	sampleArgs := append(append([]any{}, args...), activitySampleLimit)
	row := &ActivityRow{}
	err := q.QueryRowContext(ctx, `
		WITH sampled AS (
			SELECT *
			FROM request_usage
			WHERE `+where+`
			ORDER BY id DESC
			LIMIT ?
		)
		SELECT
			COUNT(*),
			COUNT(CASE WHEN status < 400 THEN 1 END),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(CAST(ROUND(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END)) AS INTEGER), 0),
			COALESCE(CAST(ROUND(AVG(CASE WHEN ttfb_ms > 0 THEN ttfb_ms END)) AS INTEGER), 0),
			COUNT(CASE WHEN capture_outcome = 'captured' THEN 1 END),
			COUNT(CASE WHEN capture_outcome = 'failed' THEN 1 END),
			COUNT(CASE WHEN capture_outcome = 'skipped' THEN 1 END)
		FROM sampled
	`, sampleArgs...).Scan(
		&row.SampleSize, &row.SuccessCount, &row.FailedCount,
		&row.AvgLatencyMs, &row.AvgTTFBMs,
		&row.CaptureCaptured, &row.CaptureFailed, &row.CaptureSkipped,
	)
	if err != nil {
		return nil, err
	}
	if row.SampleSize > 0 {
		row.FailureRate = float64(row.FailedCount) / float64(row.SampleSize)
	}
	row.P95LatencyMs, err = queryPercentileInt(ctx, q, scope, "latency_ms", 0.95, activitySampleLimit)
	if err != nil {
		return nil, err
	}
	row.P95TTFBMs, err = queryPercentileInt(ctx, q, scope, "ttfb_ms", 0.95, activitySampleLimit)
	if err != nil {
		return nil, err
	}

	err = q.QueryRowContext(ctx, `
		WITH sampled AS (
			SELECT *
			FROM request_usage
			WHERE `+where+`
			ORDER BY id DESC
			LIMIT ?
		)
		SELECT
			COALESCE(status, 0),
			COALESCE(created_at, ''),
			COALESCE(error, ''),
			COALESCE(error_class, ''),
			COALESCE(error_code, ''),
			COALESCE(endpoint, ''),
			`+effectiveModelExpr+`
		FROM sampled
		WHERE status >= 400
		ORDER BY id DESC LIMIT 1
	`, sampleArgs...).Scan(
		&row.LatestErrorStatus, &row.LatestErrorAt, &row.LatestError,
		&row.LatestErrorClass, &row.LatestErrorCode,
		&row.LatestErrorEndpoint, &row.LatestErrorModel,
	)
	if err == sql.ErrNoRows {
		return row, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}
