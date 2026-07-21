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
	ModelsReportSnapshot(ctx context.Context, scope db.ReportScope) (*db.ModelsReportData, error)
}

type SummaryReader interface {
	SummaryReportSnapshot(ctx context.Context, since time.Time) (*db.SummaryReportData, error)
}

type TimeseriesReader interface {
	TimeseriesReportSnapshot(ctx context.Context, scope db.ReportScope, bucketMin int) (*db.TimeseriesReportData, error)
}

type ImagesReader interface {
	ImageReportSnapshot(ctx context.Context, since time.Time) (*db.ImageReportData, error)
}

type OverviewReader interface {
	OverviewReportSnapshot(ctx context.Context, since, recentSince time.Time) (*db.OverviewReportData, error)
}

type ModelAssetsReader interface {
	ModelAssetsReportSnapshot(ctx context.Context, since time.Time) (*db.ModelAssetsReportData, error)
}

type KeysReader interface {
	KeysReportSnapshot(ctx context.Context, since time.Time) (*db.KeysReportData, error)
}

type ActivityReader interface {
	ActivityReport(ctx context.Context, scope db.ReportScope) (*db.ActivityRow, error)
}

type RequestsReader interface {
	RequestsReport(ctx context.Context, filter db.RequestFilter) ([]db.RequestRow, error)
}

type CaptureRuntimeReader interface {
	Snapshot() (queueDepth, dropped, parseErrors, dbErrors int64)
}

// Dependencies keeps each read capability narrow while giving the composition
// root one explicit, compile-time checked wiring object.
type Dependencies struct {
	Models      ModelsReader
	Summary     SummaryReader
	Timeseries  TimeseriesReader
	Images      ImagesReader
	Overview    OverviewReader
	Capture     CaptureRuntimeReader
	ModelAssets ModelAssetsReader
	Keys        KeysReader
	Activity    ActivityReader
	Requests    RequestsReader
	KeyLabels   map[string]string
}

// ModelsReporter is the WebUI-facing /api/models boundary.
// Composition roots inject a concrete implementation (typically *Service).
type ModelsReporter interface {
	Models(ctx context.Context, filter ModelsFilter) ([]ModelReport, error)
}

type SummaryReporter interface {
	Summary(ctx context.Context, filter SummaryFilter) (SummaryReport, error)
}

type TimeseriesReporter interface {
	Timeseries(ctx context.Context, filter TimeseriesFilter) ([]TimeseriesReport, error)
}

type ImagesReporter interface {
	ImageSummary(ctx context.Context, filter ImagesFilter) (ImageSummaryReport, error)
	ImageModels(ctx context.Context, filter ImagesFilter) ([]ImageModelReport, error)
}

type OverviewReporter interface {
	Overview(ctx context.Context, filter OverviewFilter) (OverviewReport, error)
}

type ModelAssetsReporter interface {
	ModelAssets(ctx context.Context, filter ModelAssetsFilter) (ModelAssetsReport, error)
}

type KeysReporter interface {
	Keys(ctx context.Context, filter KeysFilter) ([]KeyReport, error)
}

type ActivityReporter interface {
	Activity(ctx context.Context, filter ActivityFilter) (ActivityReport, error)
}

type RequestsReporter interface {
	Requests(ctx context.Context, filter RequestFilter) ([]RequestReport, error)
}

type CoreReporter interface {
	ModelsReporter
	SummaryReporter
	TimeseriesReporter
	ImagesReporter
	OverviewReporter
	ModelAssetsReporter
	KeysReporter
	ActivityReporter
	RequestsReporter
}

// CostEngine is the pricing surface required by core cost reports.
type CostEngine interface {
	CostText(model string, usage pricing.TextTokenUsage) (float64, bool)
	CostDimension(model, modality, channel, metric, direction, unit string, amount float64) (float64, bool)
	CostImages(model string, inputImageCount, outputImageCount int64, size string) (cost float64, known bool, sizeDefaulted bool)
	HasMultimodal(model string) bool
	HasTextPricing(model string) bool
	HasPerImagePricing(model string) bool
	HasImageTokenPricing(model string) bool
}
