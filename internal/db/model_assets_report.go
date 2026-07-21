package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ModelAssetRow is one per-model aggregate for the model assets view.
type ModelAssetRow struct {
	Model               string
	RequestCount        int64
	FailedCount         int64
	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CachedTokens        int64
	CacheCreationTokens int64
	TotalTokens         int64
	EndpointProfiles    string
	CaptureModes        string
	ModelSources        string
	LatestSeenAt        string
}

type ModelAssetsReportData struct {
	Rows             []ModelAssetRow
	TextCostBuckets  []TextCostBucketRow
	ImageCostBuckets []ImageCostBucketRow
}

func (db *DB) ModelAssetsReportSnapshot(ctx context.Context, since time.Time) (*ModelAssetsReportData, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := queryModelAssetAggregates(ctx, tx, since)
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
	return &ModelAssetsReportData{Rows: rows, TextCostBuckets: textCost, ImageCostBuckets: imageCost}, nil
}

func queryModelAssetAggregates(ctx context.Context, q modelQueryContext, since time.Time) ([]ModelAssetRow, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT
			`+effectiveModelExpr+`,
			COUNT(*),
			COUNT(CASE WHEN status >= 400 THEN 1 END),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(GROUP_CONCAT(DISTINCT COALESCE(NULLIF(endpoint_profile, ''), 'unknown')), ''),
			COALESCE(GROUP_CONCAT(DISTINCT COALESCE(NULLIF(capture_mode, ''), 'unknown')), ''),
			COALESCE(GROUP_CONCAT(DISTINCT `+modelSourceExpr+`), ''),
			COALESCE(MAX(created_at), '')
		FROM request_usage
		WHERE created_at_unix >= ?
		GROUP BY 1
		ORDER BY COUNT(*) DESC, 1 ASC
	`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("model assets query: %w", err)
	}
	defer rows.Close()

	result := make([]ModelAssetRow, 0)
	for rows.Next() {
		var row ModelAssetRow
		if err := rows.Scan(
			&row.Model, &row.RequestCount, &row.FailedCount,
			&row.InputTokens, &row.OutputTokens, &row.ReasoningTokens,
			&row.CachedTokens, &row.CacheCreationTokens, &row.TotalTokens,
			&row.EndpointProfiles, &row.CaptureModes, &row.ModelSources, &row.LatestSeenAt,
		); err != nil {
			return nil, fmt.Errorf("model assets scan: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
