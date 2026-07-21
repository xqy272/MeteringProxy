package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

func TestImageReportsUseConfiguredBillingChannels(t *testing.T) {
	prices := mustParseCostPricing(t, `
multimodal_pricing:
  token-image:
    image:
      input_per_1m: 8
      output_per_1m: 30
  per-image:
    image:
      per_image_input: 0.01
      per_image_output:
        default: 0.05
        "1K": 0.05
        "2K": 0.07
`)
	reader := &stubModelsReader{imageSnapshot: &db.ImageReportData{
		Summary: db.ImageSummaryRow{RequestCount: 2, ImageCount: 3, InputImageCount: 1, MissingUsageCount: 2},
		Models: []db.ImageModelRow{
			{Model: "per-image", Operation: "generation", RequestCount: 1, ImageCount: 2, InputImageCount: 1, MissingUsageCount: 1},
			{Model: "token-image", Operation: "generation", RequestCount: 1, ImageCount: 1, MissingUsageCount: 1},
		},
		TextCostBuckets: []db.TextCostBucketRow{
			{Model: "per-image", Operation: "generation", ImageRequest: true, RequestCount: 1, ObservedCount: 1},
			{Model: "token-image", Operation: "generation", ImageRequest: true, RequestCount: 1, ObservedCount: 1},
		},
		ImageCostBuckets: []db.ImageCostBucketRow{
			{Model: "per-image", Operation: "generation", Size: "2048x2048", RequestCount: 1, InputImageCount: 1, OutputImageCount: 2, ObservedCount: 1},
			{Model: "token-image", Operation: "generation", Size: "1024x1024", RequestCount: 1, OutputImageCount: 1, ObservedCount: 1},
		},
	}}
	svc := NewService(testDependencies(reader), prices)

	summary, err := svc.ImageSummary(context.Background(), ImagesFilter{Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("ImageSummary: %v", err)
	}
	assertCostNear(t, summary.Cost, 0.15)
	if summary.Summary.MissingUsageCount != 1 {
		t.Fatalf("summary missing usage = %d, want 1", summary.Summary.MissingUsageCount)
	}
	if !summary.CostKnown || summary.CostState != CostStatePartial || len(summary.PartialReasons) != 1 || summary.PartialReasons[0] != PartialReasonMissingUsage {
		t.Fatalf("summary cost state = %+v", summary)
	}

	models, err := svc.ImageModels(context.Background(), ImagesFilter{Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("ImageModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	byModel := make(map[string]ImageModelReport)
	for _, model := range models {
		byModel[model.Model] = model
	}
	perImage := byModel["per-image"]
	assertCostNear(t, perImage.Cost, 0.15)
	if perImage.MissingUsageCount != 0 || perImage.CostState != CostStateComplete || !perImage.CostKnown || perImage.InputImageCount != 1 {
		t.Fatalf("per-image report = %+v", perImage)
	}
	tokenImage := byModel["token-image"]
	if tokenImage.MissingUsageCount != 1 || tokenImage.CostState != CostStatePartial || !tokenImage.CostKnown {
		t.Fatalf("token-image report = %+v", tokenImage)
	}
}

func TestImageReportsPropagateSnapshotErrorAndReturnNonNilEmptyModels(t *testing.T) {
	want := errors.New("image snapshot failed")
	reader := &stubModelsReader{imageErr: want}
	svc := NewService(testDependencies(reader), &stubCostEngine{})
	if _, err := svc.ImageSummary(context.Background(), ImagesFilter{}); !errors.Is(err, want) {
		t.Fatalf("ImageSummary err=%v, want %v", err, want)
	}
	if rows, err := svc.ImageModels(context.Background(), ImagesFilter{}); !errors.Is(err, want) || rows != nil {
		t.Fatalf("ImageModels rows=%#v err=%v", rows, err)
	}

	reader.imageErr = nil
	rows, err := svc.ImageModels(context.Background(), ImagesFilter{})
	if err != nil {
		t.Fatalf("ImageModels empty: %v", err)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("rows=%#v, want non-nil empty slice", rows)
	}
}
