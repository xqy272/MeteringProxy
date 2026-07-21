package report

import (
	"context"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

// ModelsReader is the narrow DB reader surface required by the models report.
// It is defined by report as the consumer; *db.DB implements it directly.
// Implementations must load model aggregates and both source breakdowns from one
// consistent read snapshot (for example a single SQLite read-only transaction).
type ModelsReader interface {
	ModelsReportSnapshot(ctx context.Context, since time.Time) (
		models []db.ModelRow,
		modelReturned map[string]map[string]int64,
		usage map[string]map[string]int64,
		err error,
	)
}

// ModelsReporter is the WebUI-facing /api/models boundary.
// Composition roots inject a concrete implementation (typically *Service).
type ModelsReporter interface {
	Models(ctx context.Context, filter ModelsFilter) ([]ModelReport, error)
}

// CostEngine is the pricing surface required by the models report.
type CostEngine interface {
	CostWithCacheCreation(model string, inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheCreationTokens int64) (float64, bool)
}
