package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type reportQueryContext interface {
	modelQueryContext
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SummaryReportData is a consistent usage + cost snapshot for /api/summary.
type SummaryReportData struct {
	Summary          SummaryRow
	TextCostBuckets  []TextCostBucketRow
	ImageCostBuckets []ImageCostBucketRow
}

// TimeseriesReportData is a consistent usage + cost snapshot for
// /api/timeseries. Cost buckets use the same UTC bucket boundaries as usage.
type TimeseriesReportData struct {
	Rows             []TimeseriesRow
	TextCostBuckets  []TextCostBucketRow
	ImageCostBuckets []ImageCostBucketRow
}

func (db *DB) SummaryReportSnapshot(ctx context.Context, since time.Time) (*SummaryReportData, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	summary, err := querySummaryAggregate(ctx, tx, since)
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
	return &SummaryReportData{Summary: summary, TextCostBuckets: textCost, ImageCostBuckets: imageCost}, nil
}

func (db *DB) TimeseriesReportSnapshot(ctx context.Context, since time.Time, bucketMin int) (*TimeseriesReportData, error) {
	if bucketMin <= 0 {
		bucketMin = 10
	}
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := queryTimeseriesAggregates(ctx, tx, since, bucketMin)
	if err != nil {
		return nil, err
	}
	textCost, imageCost, err := queryCostBuckets(ctx, tx, CostBucketFilter{
		Since: since, BucketSeconds: int64(bucketMin) * 60,
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TimeseriesReportData{Rows: rows, TextCostBuckets: textCost, ImageCostBuckets: imageCost}, nil
}

func querySummaryAggregate(ctx context.Context, q reportQueryContext, since time.Time) (SummaryRow, error) {
	var row SummaryRow
	err := q.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
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
	return row, err
}

func queryTimeseriesAggregates(ctx context.Context, q modelQueryContext, since time.Time, bucketMin int) ([]TimeseriesRow, error) {
	bucketSec := int64(bucketMin) * 60
	bucketExpr := fmt.Sprintf(
		`strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', (created_at_unix / %d) * %d, 'unixepoch')`,
		bucketSec, bucketSec,
	)
	rows, err := q.QueryContext(ctx, `
		SELECT
			`+bucketExpr+`,
			COUNT(*),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(CAST(ROUND(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END)) AS INTEGER), 0),
			COALESCE(CAST(ROUND(AVG(CASE WHEN ttfb_ms > 0 THEN ttfb_ms END)) AS INTEGER), 0)
		FROM request_usage WHERE created_at_unix >= ?
		GROUP BY 1 ORDER BY 1 ASC
	`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]TimeseriesRow, 0)
	for rows.Next() {
		var row TimeseriesRow
		if err := rows.Scan(
			&row.Timestamp, &row.Count, &row.FailedCount,
			&row.InputTokens, &row.OutputTokens, &row.ReasoningTokens, &row.CachedTokens, &row.CacheCreationTokens, &row.TotalTokens,
			&row.AvgLatencyMs, &row.AvgTTFBMs,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
