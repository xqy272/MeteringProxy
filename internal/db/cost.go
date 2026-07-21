package db

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CostBucketFilter describes the scope and optional grouping dimensions for
// price-homogeneous usage buckets. Model is always retained because pricing is
// model-specific. BucketSeconds and GroupByKey add time/key dimensions without
// changing the billing semantics.
type CostBucketFilter struct {
	Since            time.Time
	KeyHash          string
	BucketSeconds    int64
	GroupByKey       bool
	GroupByOperation bool
	ImageOnly        bool
}

// TextCostBucketRow contains text usage that is homogeneous for tier
// selection. RequestInputTokens is one request's input token value; all rows
// folded into the bucket had that same value. InputTokens contains normalized
// billable input (regular + cached + cache creation) so aggregate clamping does
// not move tokens between price classes.
type TextCostBucketRow struct {
	Bucket              string
	KeyHash             string
	Model               string
	Operation           string
	RequestInputTokens  int64
	ImageRequest        bool
	RequestCount        int64
	BillableUsageCount  int64
	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CachedTokens        int64
	CacheCreationTokens int64
	ObservedCount       int64
	SideChannelCount    int64
	RequestOnlyCount    int64
	MissingUsageCount   int64
	UnsupportedCount    int64
	ConflictCount       int64
}

// ImageCostBucketRow keeps the image request metadata and token channels that
// affect multimodal pricing. Raw sizes remain intact; pricing owns size
// normalization and reports whether the default rate was used.
type ImageCostBucketRow struct {
	Bucket             string
	KeyHash            string
	Model              string
	Operation          string
	Size               string
	RequestCount       int64
	FailedCount        int64
	InputImageCount    int64
	OutputImageCount   int64
	PartialImageCount  int64
	MissingOutputCount int64
	TokenUsageCount    int64
	InputTextTokens    int64
	CachedTextTokens   int64
	InputImageTokens   int64
	CachedImageTokens  int64
	InputMixedTokens   int64
	CachedMixedTokens  int64
	OutputTextTokens   int64
	OutputImageTokens  int64
	OutputMixedTokens  int64
	ObservedCount      int64
	SideChannelCount   int64
	RequestOnlyCount   int64
	MissingUsageCount  int64
	UnsupportedCount   int64
	ConflictCount      int64
}

// CostBucketsContext loads text and image cost buckets from one consistent
// SQLite read snapshot.
func (db *DB) CostBucketsContext(ctx context.Context, filter CostBucketFilter) ([]TextCostBucketRow, []ImageCostBucketRow, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	textRows, imageRows, err := queryCostBuckets(ctx, tx, filter)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return textRows, imageRows, nil
}

func queryCostBuckets(ctx context.Context, q modelQueryContext, filter CostBucketFilter) ([]TextCostBucketRow, []ImageCostBucketRow, error) {
	textRows, err := queryTextCostBuckets(ctx, q, filter)
	if err != nil {
		return nil, nil, err
	}
	imageRows, err := queryImageCostBuckets(ctx, q, filter)
	if err != nil {
		return nil, nil, err
	}
	return textRows, imageRows, nil
}

func costBucketExpressions(filter CostBucketFilter) (bucketExpr, keyExpr, operationExpr, where string, args []any) {
	bucketExpr = `''`
	if filter.BucketSeconds > 0 {
		seconds := strconv.FormatInt(filter.BucketSeconds, 10)
		bucketExpr = fmt.Sprintf(`strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', (ru.created_at_unix / %s) * %s, 'unixepoch')`, seconds, seconds)
	}
	keyExpr = `''`
	if filter.GroupByKey {
		keyExpr = `COALESCE(NULLIF(TRIM(ru.api_key_hash), ''), 'unknown')`
	}
	operationExpr = `''`
	if filter.GroupByOperation {
		operationExpr = `COALESCE((SELECT NULLIF(TRIM(iu_op.operation), '') FROM image_usage iu_op WHERE iu_op.request_usage_id = ru.id ORDER BY iu_op.id ASC LIMIT 1), 'unknown')`
	}

	where = `ru.created_at_unix >= ?`
	args = append(args, filter.Since.Unix())
	switch {
	case len(filter.KeyHash) == 0:
	case filter.KeyHash == "unknown":
		where += ` AND NULLIF(TRIM(ru.api_key_hash), '') IS NULL`
	default:
		where += ` AND ru.api_key_hash = ?`
		args = append(args, filter.KeyHash)
	}
	if filter.ImageOnly {
		where += ` AND EXISTS (SELECT 1 FROM image_usage iu_scope WHERE iu_scope.request_usage_id = ru.id)`
	}
	return bucketExpr, keyExpr, operationExpr, where, args
}

func queryTextCostBuckets(ctx context.Context, q modelQueryContext, filter CostBucketFilter) ([]TextCostBucketRow, error) {
	bucketExpr, keyExpr, operationExpr, where, args := costBucketExpressions(filter)
	query := fmt.Sprintf(`
		WITH raw AS (
			SELECT
				%s AS bucket,
				%s AS key_hash,
				%s AS model,
				%s AS operation,
				MAX(COALESCE(ru.input_tokens, 0), 0) AS request_input_tokens,
				MAX(COALESCE(ru.cached_tokens, 0), 0) AS cached_raw,
				MAX(COALESCE(ru.cache_creation_tokens, 0), 0) AS cache_creation_raw,
				MAX(COALESCE(ru.output_tokens, 0), 0) AS output_tokens,
				MAX(COALESCE(ru.reasoning_tokens, 0), 0) AS reasoning_tokens,
				CASE WHEN EXISTS (
					SELECT 1 FROM image_usage iu WHERE iu.request_usage_id = ru.id
				) THEN 1 ELSE 0 END AS image_request,
				CASE WHEN EXISTS (
					SELECT 1 FROM side_usage_events sue
					WHERE sue.matched_request_usage_id = ru.id AND sue.match_status = 'conflict'
				) THEN 1 ELSE 0 END AS conflict,
				COALESCE(ru.usage_source, '') AS usage_source,
				COALESCE(ru.capture_mode, '') AS capture_mode,
				COALESCE(ru.capture_outcome, '') AS capture_outcome
			FROM request_usage ru
			WHERE %s
		), creation_normalized AS (
			SELECT *,
				CASE
					WHEN request_input_tokens > 0 THEN MIN(cache_creation_raw, request_input_tokens)
					ELSE cache_creation_raw
				END AS cache_creation_tokens
			FROM raw
		), input_normalized AS (
			SELECT *,
				CASE
					WHEN request_input_tokens > 0 THEN MIN(cached_raw, MAX(request_input_tokens - cache_creation_tokens, 0))
					ELSE cached_raw
				END AS cached_tokens
			FROM creation_normalized
		), classified AS (
			SELECT *,
				CASE
					WHEN request_input_tokens > 0 THEN request_input_tokens
					ELSE cached_tokens + cache_creation_tokens
				END AS billable_input_tokens,
				CASE
					WHEN conflict = 1 THEN 'conflict'
					WHEN usage_source = 'cliproxy_side_channel' THEN 'side_channel'
					WHEN capture_mode = 'request_only' THEN 'request_only'
					WHEN capture_mode = 'passthrough' THEN 'unsupported'
					WHEN capture_outcome = 'captured' THEN 'observed'
					WHEN capture_outcome = '' AND (
						request_input_tokens > 0 OR output_tokens > 0 OR reasoning_tokens > 0 OR
						cached_tokens > 0 OR cache_creation_tokens > 0
					) THEN 'observed'
					ELSE 'missing_usage'
				END AS confidence
			FROM input_normalized
		)
		SELECT
			bucket, key_hash, model, operation, request_input_tokens, image_request,
			COUNT(*),
			SUM(CASE WHEN billable_input_tokens > 0 OR output_tokens > 0 OR reasoning_tokens > 0 THEN 1 ELSE 0 END),
			COALESCE(SUM(billable_input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			SUM(CASE WHEN confidence = 'observed' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'side_channel' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'request_only' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'missing_usage' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'unsupported' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'conflict' THEN 1 ELSE 0 END)
		FROM classified
		GROUP BY 1, 2, 3, 4, 5, 6
		ORDER BY 1, 2, 3, 4, 5, 6
	`, bucketExpr, keyExpr, effectiveModelExprWithAlias("ru"), operationExpr, where)

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]TextCostBucketRow, 0)
	for rows.Next() {
		var row TextCostBucketRow
		if err := rows.Scan(
			&row.Bucket, &row.KeyHash, &row.Model, &row.Operation, &row.RequestInputTokens, &row.ImageRequest,
			&row.RequestCount, &row.BillableUsageCount, &row.InputTokens, &row.OutputTokens, &row.ReasoningTokens,
			&row.CachedTokens, &row.CacheCreationTokens,
			&row.ObservedCount, &row.SideChannelCount, &row.RequestOnlyCount,
			&row.MissingUsageCount, &row.UnsupportedCount, &row.ConflictCount,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func queryImageCostBuckets(ctx context.Context, q modelQueryContext, filter CostBucketFilter) ([]ImageCostBucketRow, error) {
	bucketExpr, keyExpr, _, where, args := costBucketExpressions(filter)
	query := fmt.Sprintf(`
		WITH dimensions AS (
			SELECT
				request_usage_id,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'text' AND direction = 'input' THEN amount ELSE 0 END), 0)) AS INTEGER) AS input_text,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'text' AND direction = 'cached_input' THEN amount ELSE 0 END), 0)) AS INTEGER) AS cached_text,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'image' AND direction = 'input' THEN amount ELSE 0 END), 0)) AS INTEGER) AS input_image,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'image' AND direction = 'cached_input' THEN amount ELSE 0 END), 0)) AS INTEGER) AS cached_image,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'mixed' AND direction = 'input' THEN amount ELSE 0 END), 0)) AS INTEGER) AS input_mixed,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'mixed' AND direction = 'cached_input' THEN amount ELSE 0 END), 0)) AS INTEGER) AS cached_mixed,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'text' AND direction = 'output' THEN amount ELSE 0 END), 0)) AS INTEGER) AS output_text,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'image' AND direction = 'output' THEN amount ELSE 0 END), 0)) AS INTEGER) AS output_image,
				CAST(ROUND(COALESCE(SUM(CASE WHEN channel = 'mixed' AND direction = 'output' THEN amount ELSE 0 END), 0)) AS INTEGER) AS output_mixed
			FROM usage_dimensions
			WHERE created_at_unix >= ? AND modality = 'image' AND metric = 'tokens' AND unit = 'token'
			GROUP BY request_usage_id
		), raw AS (
			SELECT
				%s AS bucket,
				%s AS key_hash,
				%s AS model,
				COALESCE(NULLIF(TRIM(iu.operation), ''), 'unknown') AS operation,
				COALESCE(TRIM(iu.size), '') AS size,
				COALESCE(ru.status, 0) AS status,
				MAX(COALESCE(iu.input_image_count, 0), 0) AS input_image_count,
				MAX(COALESCE(iu.image_count, 0), 0) AS output_image_count,
				MAX(COALESCE(iu.partial_image_count, 0), 0) AS partial_image_count,
				MAX(COALESCE(d.input_text, 0), 0) AS input_text,
				MAX(COALESCE(d.cached_text, 0), 0) AS cached_text,
				MAX(COALESCE(d.input_image, 0), 0) AS input_image,
				MAX(COALESCE(d.cached_image, 0), 0) AS cached_image,
				MAX(COALESCE(d.input_mixed, 0), 0) AS input_mixed,
				MAX(COALESCE(d.cached_mixed, 0), 0) AS cached_mixed,
				MAX(COALESCE(d.output_text, 0), 0) AS output_text,
				MAX(COALESCE(d.output_image, 0), 0) AS output_image,
				MAX(COALESCE(d.output_mixed, 0), 0) AS output_mixed,
				CASE WHEN EXISTS (
					SELECT 1 FROM side_usage_events sue
					WHERE sue.matched_request_usage_id = ru.id AND sue.match_status = 'conflict'
				) THEN 1 ELSE 0 END AS conflict,
				COALESCE(ru.usage_source, '') AS usage_source,
				COALESCE(ru.capture_mode, '') AS capture_mode,
				COALESCE(ru.capture_outcome, '') AS capture_outcome
			FROM image_usage iu
			JOIN request_usage ru ON ru.id = iu.request_usage_id
			LEFT JOIN dimensions d ON d.request_usage_id = ru.id
			WHERE %s
		), normalized AS (
			SELECT *,
				CASE WHEN input_text > 0 THEN MAX(input_text - MIN(cached_text, input_text), 0) ELSE 0 END AS regular_text,
				CASE WHEN input_text > 0 THEN MIN(cached_text, input_text) ELSE cached_text END AS billed_cached_text,
				CASE WHEN input_image > 0 THEN MAX(input_image - MIN(cached_image, input_image), 0) ELSE 0 END AS regular_image,
				CASE WHEN input_image > 0 THEN MIN(cached_image, input_image) ELSE cached_image END AS billed_cached_image,
				CASE WHEN input_mixed > 0 THEN MAX(input_mixed - MIN(cached_mixed, input_mixed), 0) ELSE 0 END AS regular_mixed,
				CASE WHEN input_mixed > 0 THEN MIN(cached_mixed, input_mixed) ELSE cached_mixed END AS billed_cached_mixed,
				CASE
					WHEN conflict = 1 THEN 'conflict'
					WHEN usage_source = 'cliproxy_side_channel' THEN 'side_channel'
					WHEN capture_mode = 'request_only' THEN 'request_only'
					WHEN capture_mode = 'passthrough' THEN 'unsupported'
					WHEN capture_outcome = 'captured' THEN 'observed'
					WHEN capture_outcome = '' AND (
						input_text > 0 OR cached_text > 0 OR input_image > 0 OR cached_image > 0 OR
						input_mixed > 0 OR cached_mixed > 0 OR output_text > 0 OR output_image > 0 OR output_mixed > 0
					) THEN 'observed'
					ELSE 'missing_usage'
				END AS confidence
			FROM raw
		)
		SELECT
			bucket, key_hash, model, operation, size,
			COUNT(*),
			SUM(CASE WHEN status >= 400 THEN 1 ELSE 0 END),
			COALESCE(SUM(input_image_count), 0),
			COALESCE(SUM(output_image_count), 0),
			COALESCE(SUM(partial_image_count), 0),
			SUM(CASE WHEN status >= 200 AND status < 400 AND capture_mode != 'request_only' AND output_image_count <= 0 THEN 1 ELSE 0 END),
			SUM(CASE WHEN regular_text > 0 OR billed_cached_text > 0 OR regular_image > 0 OR billed_cached_image > 0 OR regular_mixed > 0 OR billed_cached_mixed > 0 OR output_text > 0 OR output_image > 0 OR output_mixed > 0 THEN 1 ELSE 0 END),
			COALESCE(SUM(regular_text), 0),
			COALESCE(SUM(billed_cached_text), 0),
			COALESCE(SUM(regular_image), 0),
			COALESCE(SUM(billed_cached_image), 0),
			COALESCE(SUM(regular_mixed), 0),
			COALESCE(SUM(billed_cached_mixed), 0),
			COALESCE(SUM(output_text), 0),
			COALESCE(SUM(output_image), 0),
			COALESCE(SUM(output_mixed), 0),
			SUM(CASE WHEN confidence = 'observed' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'side_channel' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'request_only' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'missing_usage' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'unsupported' THEN 1 ELSE 0 END),
			SUM(CASE WHEN confidence = 'conflict' THEN 1 ELSE 0 END)
		FROM normalized
		GROUP BY 1, 2, 3, 4, 5
		ORDER BY 1, 2, 3, 4, 5
	`, bucketExpr, keyExpr, effectiveModelExprWithAlias("ru"), where)

	imageArgs := make([]any, 0, len(args)+1)
	imageArgs = append(imageArgs, filter.Since.Unix())
	imageArgs = append(imageArgs, args...)
	rows, err := q.QueryContext(ctx, query, imageArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ImageCostBucketRow, 0)
	for rows.Next() {
		var row ImageCostBucketRow
		if err := rows.Scan(
			&row.Bucket, &row.KeyHash, &row.Model, &row.Operation, &row.Size,
			&row.RequestCount, &row.FailedCount, &row.InputImageCount, &row.OutputImageCount,
			&row.PartialImageCount, &row.MissingOutputCount, &row.TokenUsageCount,
			&row.InputTextTokens, &row.CachedTextTokens, &row.InputImageTokens,
			&row.CachedImageTokens, &row.InputMixedTokens, &row.CachedMixedTokens,
			&row.OutputTextTokens, &row.OutputImageTokens, &row.OutputMixedTokens,
			&row.ObservedCount, &row.SideChannelCount, &row.RequestOnlyCount,
			&row.MissingUsageCount, &row.UnsupportedCount, &row.ConflictCount,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func effectiveModelExprWithAlias(alias string) string {
	alias = strings.TrimSpace(alias)
	if len(alias) == 0 {
		return effectiveModelExpr
	}
	prefix := alias + "."
	return `COALESCE(NULLIF(TRIM(` + prefix + `model_returned), ''), NULLIF(TRIM(` + prefix + `model_requested), ''), 'unidentified')`
}
