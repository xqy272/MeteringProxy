package report

import (
	"context"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/pricing"
)

// ModelsReader is the narrow DB reader surface required by the models report.
// It is defined by report as the consumer; *db.DB implements it directly.
// Implementations must load model aggregates and both source breakdowns from one
// consistent read snapshot (for example a single SQLite read-only transaction).
type ModelsReader interface {
	ModelsReportSnapshot(ctx context.Context, since time.Time) (*db.ModelsReportData, error)
}

// ModelsReporter is the WebUI-facing /api/models boundary.
// Composition roots inject a concrete implementation (typically *Service).
type ModelsReporter interface {
	Models(ctx context.Context, filter ModelsFilter) ([]ModelReport, error)
}

// CostEngine is the pricing surface required by the models report.
type CostEngine interface {
	CostText(model string, usage pricing.TextTokenUsage) (float64, bool)
	CostDimension(model, modality, channel, metric, direction, unit string, amount float64) (float64, bool)
	CostImages(model string, inputImageCount, outputImageCount int64, size string) (cost float64, known bool, sizeDefaulted bool)
	HasMultimodal(model string) bool
	HasPerImagePricing(model string) bool
	HasImageTokenPricing(model string) bool
}
