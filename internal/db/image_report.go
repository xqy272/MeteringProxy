package db

import (
	"context"
	"database/sql"
	"time"
)

// ImageReportData is a consistent snapshot shared by image summary and model
// reports. Cost buckets are image-only and preserve operation/size where needed.
type ImageReportData struct {
	Summary          ImageSummaryRow
	Models           []ImageModelRow
	TextCostBuckets  []TextCostBucketRow
	ImageCostBuckets []ImageCostBucketRow
}

func (db *DB) ImageReportSnapshot(ctx context.Context, since time.Time) (*ImageReportData, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	summary, err := queryImageSummaryAggregate(ctx, tx, since)
	if err != nil {
		return nil, err
	}
	models, err := queryImageModelAggregates(ctx, tx, since)
	if err != nil {
		return nil, err
	}
	textCost, imageCost, err := queryCostBuckets(ctx, tx, CostBucketFilter{
		Since: since, GroupByOperation: true, ImageOnly: true,
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ImageReportData{Summary: summary, Models: models, TextCostBuckets: textCost, ImageCostBuckets: imageCost}, nil
}

func queryImageSummaryAggregate(ctx context.Context, q reportQueryContext, since time.Time) (ImageSummaryRow, error) {
	var row ImageSummaryRow
	err := q.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(CASE WHEN ru.status >= 400 THEN 1 END),
			COALESCE(SUM(iu.image_count), 0),
			COALESCE(SUM(iu.partial_image_count), 0),
			COALESCE(SUM(iu.input_image_count), 0),
			COUNT(CASE WHEN ru.capture_mode != 'request_only' AND (
				ru.capture_outcome != 'captured'
				OR NOT EXISTS (
					SELECT 1 FROM usage_dimensions ud
					WHERE ud.request_usage_id = ru.id
						AND ud.modality = 'image'
						AND ud.metric = 'tokens'
						AND ud.amount > 0
				)
			) THEN 1 END)
		FROM image_usage iu
		JOIN request_usage ru ON ru.id = iu.request_usage_id
		WHERE ru.created_at_unix >= ?
	`, since.Unix()).Scan(
		&row.RequestCount,
		&row.FailedCount,
		&row.ImageCount,
		&row.PartialImageCount,
		&row.InputImageCount,
		&row.MissingUsageCount,
	)
	if err != nil {
		return ImageSummaryRow{}, err
	}

	dimRows, err := q.QueryContext(ctx, `
		SELECT channel, direction, CAST(ROUND(COALESCE(SUM(amount), 0)) AS INTEGER)
		FROM usage_dimensions
		WHERE created_at_unix >= ?
			AND modality = 'image'
			AND metric = 'tokens'
			AND unit = 'token'
		GROUP BY channel, direction
	`, since.Unix())
	if err != nil {
		return ImageSummaryRow{}, err
	}
	defer dimRows.Close()
	for dimRows.Next() {
		var channel, direction string
		var amount int64
		if err := dimRows.Scan(&channel, &direction, &amount); err != nil {
			return ImageSummaryRow{}, err
		}
		switch {
		case channel == "text" && direction == "input":
			row.InputTextTokens += amount
		case channel == "image" && direction == "input":
			row.InputImageTokens += amount
		case channel == "text" && direction == "cached_input":
			row.CachedTextTokens += amount
		case channel == "image" && direction == "cached_input":
			row.CachedImageTokens += amount
		case channel == "mixed" && direction == "cached_input":
			row.CachedMixedTokens += amount
		case channel == "image" && direction == "output":
			row.OutputImageTokens += amount
		case channel == "mixed" && direction == "input":
			row.InputTextTokens += amount
		}
		if direction == "input" || direction == "output" {
			row.TotalTokens += amount
		}
	}
	if err := dimRows.Err(); err != nil {
		return ImageSummaryRow{}, err
	}
	return row, nil
}

func queryImageModelAggregates(ctx context.Context, q modelQueryContext, since time.Time) ([]ImageModelRow, error) {
	rows, err := q.QueryContext(ctx, `
		WITH dim AS (
			SELECT
				request_usage_id,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'text' AND direction = 'input' AND metric = 'tokens' THEN amount ELSE 0 END), 0)) AS INTEGER) AS input_text_tokens,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'image' AND direction = 'input' AND metric = 'tokens' THEN amount ELSE 0 END), 0)) AS INTEGER) AS input_image_tokens,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'text' AND direction = 'cached_input' AND metric = 'tokens' THEN amount ELSE 0 END), 0)) AS INTEGER) AS cached_text_tokens,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'image' AND direction = 'cached_input' AND metric = 'tokens' THEN amount ELSE 0 END), 0)) AS INTEGER) AS cached_image_tokens,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'mixed' AND direction = 'cached_input' AND metric = 'tokens' THEN amount ELSE 0 END), 0)) AS INTEGER) AS cached_mixed_tokens,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'image' AND direction = 'output' AND metric = 'tokens' THEN amount ELSE 0 END), 0)) AS INTEGER) AS output_image_tokens,
				CAST(ROUND(COALESCE(SUM(CASE WHEN metric = 'tokens' AND direction IN ('input', 'output') THEN amount ELSE 0 END), 0)) AS INTEGER) AS total_tokens
			FROM usage_dimensions
			WHERE modality = 'image' AND created_at_unix >= ?
			GROUP BY request_usage_id
		)
		SELECT
			`+effectiveModelExprWithAlias("ru")+`,
			COALESCE(NULLIF(TRIM(iu.operation), ''), 'unknown'),
			COUNT(*),
			COUNT(CASE WHEN ru.status >= 400 THEN 1 END),
			COALESCE(SUM(iu.image_count), 0),
			COALESCE(SUM(iu.partial_image_count), 0),
			COALESCE(SUM(iu.input_image_count), 0),
			COALESCE(SUM(dim.input_text_tokens), 0),
			COALESCE(SUM(dim.input_image_tokens), 0),
			COALESCE(SUM(dim.cached_text_tokens), 0),
			COALESCE(SUM(dim.cached_image_tokens), 0),
			COALESCE(SUM(dim.cached_mixed_tokens), 0),
			COALESCE(SUM(dim.output_image_tokens), 0),
			COALESCE(SUM(dim.total_tokens), 0),
			COUNT(CASE WHEN ru.capture_mode != 'request_only' AND (
				ru.capture_outcome != 'captured' OR COALESCE(dim.total_tokens, 0) = 0
			) THEN 1 END)
		FROM image_usage iu
		JOIN request_usage ru ON ru.id = iu.request_usage_id
		LEFT JOIN dim ON dim.request_usage_id = ru.id
		WHERE ru.created_at_unix >= ?
		GROUP BY 1, 2
		ORDER BY COUNT(*) DESC, 1 ASC, 2 ASC
	`, since.Unix(), since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ImageModelRow, 0)
	for rows.Next() {
		var row ImageModelRow
		if err := rows.Scan(
			&row.Model,
			&row.Operation,
			&row.RequestCount,
			&row.FailedCount,
			&row.ImageCount,
			&row.PartialImageCount,
			&row.InputImageCount,
			&row.InputTextTokens,
			&row.InputImageTokens,
			&row.CachedTextTokens,
			&row.CachedImageTokens,
			&row.CachedMixedTokens,
			&row.OutputImageTokens,
			&row.TotalTokens,
			&row.MissingUsageCount,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
