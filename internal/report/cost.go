package report

import (
	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/pricing"
)

// CostState describes both price coverage and usage completeness.
type CostState string

const (
	CostStateComplete    CostState = "complete"
	CostStatePartial     CostState = "partial"
	CostStateUnavailable CostState = "unavailable"
)

// PartialReason is a stable machine-readable reason for an incomplete cost.
type PartialReason string

const (
	PartialReasonUnpricedModel      PartialReason = "unpriced_model"
	PartialReasonMissingUsage       PartialReason = "missing_usage"
	PartialReasonRequestOnly        PartialReason = "request_only"
	PartialReasonUnsupported        PartialReason = "unsupported"
	PartialReasonUsageConflict      PartialReason = "usage_conflict"
	PartialReasonImageCountMissing  PartialReason = "image_count_missing"
	PartialReasonImageSizeDefaulted PartialReason = "image_size_defaulted"
	PartialReasonCostQueryFailed    PartialReason = "cost_query_failed"
)

var partialReasonOrder = []PartialReason{
	PartialReasonUnpricedModel,
	PartialReasonMissingUsage,
	PartialReasonRequestOnly,
	PartialReasonUnsupported,
	PartialReasonUsageConflict,
	PartialReasonImageCountMissing,
	PartialReasonImageSizeDefaulted,
	PartialReasonCostQueryFailed,
}

// UsageConfidenceCounts is shared by model and Key reports.
type UsageConfidenceCounts struct {
	Observed     int64 `json:"observed"`
	SideChannel  int64 `json:"side_channel"`
	RequestOnly  int64 `json:"request_only"`
	MissingUsage int64 `json:"missing_usage"`
	Unsupported  int64 `json:"unsupported"`
	Conflict     int64 `json:"conflict"`
}

// CostResult is the common cost contract used by every report grouping.
// Amount is the known subtotal even when State is partial.
type CostResult struct {
	Amount                float64
	CostKnown             bool
	State                 CostState
	UnpricedModels        int64
	PartialReasons        []PartialReason
	UsageConfidenceCounts UsageConfidenceCounts
}

// CostGroup identifies one report aggregation cell. Empty dimensions mean the
// caller did not request that grouping.
type CostGroup struct {
	Bucket  string
	KeyHash string
	Model   string
}

type costAccumulator struct {
	amount                 float64
	priceKnown             bool
	textPriceMatched       bool
	imageExpectedTextUsage int64
	imageTextUsage         int64
	unpricedModels         map[string]struct{}
	reasons                map[PartialReason]struct{}
	confidence             UsageConfidenceCounts
}

func newCostAccumulator() *costAccumulator {
	return &costAccumulator{
		priceKnown:     true,
		unpricedModels: make(map[string]struct{}),
		reasons:        make(map[PartialReason]struct{}),
	}
}

func evaluateCostBuckets(engine CostEngine, textRows []db.TextCostBucketRow, imageRows []db.ImageCostBucketRow) map[CostGroup]CostResult {
	accumulators := make(map[CostGroup]*costAccumulator)
	get := func(group CostGroup) *costAccumulator {
		acc := accumulators[group]
		if acc == nil {
			acc = newCostAccumulator()
			accumulators[group] = acc
		}
		return acc
	}

	for _, row := range textRows {
		group := CostGroup{Bucket: row.Bucket, KeyHash: row.KeyHash, Model: row.Model}
		acc := get(group)
		acc.addConfidence(row.ObservedCount, row.SideChannelCount, row.RequestOnlyCount, row.MissingUsageCount, row.UnsupportedCount, row.ConflictCount)
		if row.ImageRequest {
			acc.imageExpectedTextUsage += row.ObservedCount + row.SideChannelCount
			acc.imageTextUsage += row.BillableUsageCount
		}

		// A matched multimodal image request is billed only from its image
		// dimensions below. This is the image/text double-counting boundary.
		if row.ImageRequest && engine.HasMultimodal(row.Model) {
			continue
		}

		cost, known := engine.CostText(row.Model, pricing.TextTokenUsage{
			InputTokens:         row.InputTokens,
			OutputTokens:        row.OutputTokens,
			ReasoningTokens:     row.ReasoningTokens,
			CachedTokens:        row.CachedTokens,
			CacheCreationTokens: row.CacheCreationTokens,
			RequestInputTokens:  row.RequestInputTokens,
		})
		if known {
			acc.textPriceMatched = true
			acc.amount += cost
		} else if textBucketBillable(row) {
			acc.markUnpriced(row.Model)
		}
	}

	for _, row := range imageRows {
		group := CostGroup{Bucket: row.Bucket, KeyHash: row.KeyHash, Model: row.Model}
		acc := get(group)

		if row.MissingOutputCount > 0 {
			acc.addReason(PartialReasonImageCountMissing)
		}

		if !engine.HasMultimodal(row.Model) {
			// Text pricing remains a valid fallback for image-producing requests
			// whose model has no multimodal entry (for example blended-token
			// models). Counts are metadata unless per-image pricing is enabled.
			if imageBucketBillable(row) && !acc.textPriceMatched {
				acc.markUnpriced(row.Model)
			}
			if acc.imageTextUsage < acc.imageExpectedTextUsage {
				acc.addReason(PartialReasonMissingUsage)
			}
			continue
		}

		hasTokenPricing := engine.HasImageTokenPricing(row.Model)
		hasPerImagePricing := engine.HasPerImagePricing(row.Model)
		if hasTokenPricing {
			acc.addImageDimension(engine, row.Model, "text", "input", row.InputTextTokens)
			acc.addImageDimension(engine, row.Model, "text", "cached_input", row.CachedTextTokens)
			acc.addImageDimension(engine, row.Model, "image", "input", row.InputImageTokens)
			acc.addImageDimension(engine, row.Model, "image", "cached_input", row.CachedImageTokens)
			acc.addImageDimension(engine, row.Model, "mixed", "input", row.InputMixedTokens)
			acc.addImageDimension(engine, row.Model, "mixed", "cached_input", row.CachedMixedTokens)
			acc.addImageDimension(engine, row.Model, "text", "output", row.OutputTextTokens)
			acc.addImageDimension(engine, row.Model, "image", "output", row.OutputImageTokens)
			acc.addImageDimension(engine, row.Model, "mixed", "output", row.OutputMixedTokens)
			if row.TokenUsageCount < row.ObservedCount+row.SideChannelCount {
				acc.addReason(PartialReasonMissingUsage)
			}
		}

		if hasPerImagePricing {
			imageCost, known, defaulted := engine.CostImages(row.Model, row.InputImageCount, row.OutputImageCount, row.Size)
			acc.amount += imageCost
			if !known && (row.InputImageCount > 0 || row.OutputImageCount > 0) {
				acc.markUnpriced(row.Model)
			}
			if defaulted && row.OutputImageCount > 0 {
				acc.addReason(PartialReasonImageSizeDefaulted)
			}
		}
		if !hasTokenPricing && !hasPerImagePricing && imageBucketBillable(row) {
			acc.markUnpriced(row.Model)
		}
	}

	results := make(map[CostGroup]CostResult, len(accumulators))
	for group, acc := range accumulators {
		results[group] = acc.result()
	}
	return results
}

func (a *costAccumulator) addConfidence(observed, sideChannel, requestOnly, missing, unsupported, conflict int64) {
	a.confidence.Observed += observed
	a.confidence.SideChannel += sideChannel
	a.confidence.RequestOnly += requestOnly
	a.confidence.MissingUsage += missing
	a.confidence.Unsupported += unsupported
	a.confidence.Conflict += conflict
	if requestOnly > 0 {
		a.addReason(PartialReasonRequestOnly)
	}
	if missing > 0 {
		a.addReason(PartialReasonMissingUsage)
	}
	if unsupported > 0 {
		a.addReason(PartialReasonUnsupported)
	}
	if conflict > 0 {
		a.addReason(PartialReasonUsageConflict)
	}
}

func (a *costAccumulator) addImageDimension(engine CostEngine, model, channel, direction string, amount int64) {
	if amount <= 0 {
		return
	}
	cost, known := engine.CostDimension(model, "image", channel, "tokens", direction, "token", float64(amount))
	if !known {
		a.markUnpriced(model)
		return
	}
	a.amount += cost
}

func (a *costAccumulator) markUnpriced(model string) {
	a.priceKnown = false
	a.unpricedModels[model] = struct{}{}
	a.addReason(PartialReasonUnpricedModel)
}

func (a *costAccumulator) addReason(reason PartialReason) {
	a.reasons[reason] = struct{}{}
}

func (a *costAccumulator) result() CostResult {
	reasons := make([]PartialReason, 0, len(a.reasons))
	for _, reason := range partialReasonOrder {
		if _, ok := a.reasons[reason]; ok {
			reasons = append(reasons, reason)
		}
	}
	state := CostStateComplete
	if len(reasons) > 0 {
		state = CostStatePartial
	}
	return CostResult{
		Amount:                a.amount,
		CostKnown:             a.priceKnown,
		State:                 state,
		UnpricedModels:        int64(len(a.unpricedModels)),
		PartialReasons:        reasons,
		UsageConfidenceCounts: a.confidence,
	}
}

func textBucketBillable(row db.TextCostBucketRow) bool {
	return row.InputTokens > 0 || row.OutputTokens > 0 || row.ReasoningTokens > 0 || row.CachedTokens > 0 || row.CacheCreationTokens > 0
}

func imageBucketBillable(row db.ImageCostBucketRow) bool {
	return row.InputImageCount > 0 || row.OutputImageCount > 0 ||
		row.InputTextTokens > 0 || row.CachedTextTokens > 0 ||
		row.InputImageTokens > 0 || row.CachedImageTokens > 0 ||
		row.InputMixedTokens > 0 || row.CachedMixedTokens > 0 ||
		row.OutputTextTokens > 0 || row.OutputImageTokens > 0 || row.OutputMixedTokens > 0
}

func completeZeroCost() CostResult {
	return CostResult{CostKnown: true, State: CostStateComplete, PartialReasons: []PartialReason{}}
}

func applyModelCostResult(report *ModelReport, result CostResult) {
	report.Cost = result.Amount
	report.CostKnown = result.CostKnown
	report.CostState = result.State
	report.UnpricedModels = result.UnpricedModels
	report.PartialReasons = result.PartialReasons
	if report.PartialReasons == nil {
		report.PartialReasons = []PartialReason{}
	}
	report.UsageConfidenceCounts = result.UsageConfidenceCounts
}
