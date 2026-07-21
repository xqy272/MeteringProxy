package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type KeyRow struct {
	KeyHash             string
	RequestCount        int64
	FailedCount         int64
	ModelCount          int64
	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CachedTokens        int64
	CacheCreationTokens int64
	TotalTokens         int64
	AvgLatencyMs        int64
	AvgTTFBMs           int64
	LatestSeenAt        string
}

type KeysReportData struct {
	Rows             []KeyRow
	TextCostBuckets  []TextCostBucketRow
	ImageCostBuckets []ImageCostBucketRow
}

func (db *DB) KeysReportSnapshot(ctx context.Context, since time.Time) (*KeysReportData, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := queryKeyAggregates(ctx, tx, since)
	if err != nil {
		return nil, err
	}
	textCost, imageCost, err := queryCostBuckets(ctx, tx, CostBucketFilter{Since: since, GroupByKey: true})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &KeysReportData{Rows: rows, TextCostBuckets: textCost, ImageCostBuckets: imageCost}, nil
}

func queryKeyAggregates(ctx context.Context, q modelQueryContext, since time.Time) ([]KeyRow, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT
			COALESCE(NULLIF(TRIM(api_key_hash), ''), 'unknown'),
			COUNT(*),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COUNT(DISTINCT `+effectiveModelExpr+`),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(CAST(ROUND(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END)) AS INTEGER), 0),
			COALESCE(CAST(ROUND(AVG(CASE WHEN ttfb_ms > 0 THEN ttfb_ms END)) AS INTEGER), 0),
			COALESCE(MAX(created_at), '')
		FROM request_usage
		WHERE created_at_unix >= ?
		GROUP BY 1
		ORDER BY COUNT(*) DESC, 1 ASC
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("keys query: %w", err)
	}
	defer rows.Close()

	result := make([]KeyRow, 0)
	for rows.Next() {
		var row KeyRow
		if err := rows.Scan(
			&row.KeyHash, &row.RequestCount, &row.FailedCount, &row.ModelCount,
			&row.InputTokens, &row.OutputTokens, &row.ReasoningTokens,
			&row.CachedTokens, &row.CacheCreationTokens, &row.TotalTokens,
			&row.AvgLatencyMs, &row.AvgTTFBMs, &row.LatestSeenAt,
		); err != nil {
			return nil, fmt.Errorf("keys scan: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
