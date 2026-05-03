package db

import (
	"fmt"
	"math/rand"
	"time"
)

// Capture mode / outcome constants — duplicated from internal/event to avoid an
// import cycle (event/mapping.go imports db).
const (
	demoCaptureUsageMetered = "usage_metered"
	demoOutcomeCaptured     = "captured"
	demoOutcomeFailed       = "failed"
	demoMeteringLLMTokens   = "llm_tokens"
)

func SeedDemo(database *DB) error {
	now := time.Now().UTC()
	var records []UsageRecord

	// Models that match pricing.yaml so cost coverage is meaningful.
	models := []string{"gpt-5.4", "gpt-5.4-mini", "claude-sonnet-4-6", "claude-haiku-4-5", "deepseek-v4-pro", "deepseek-v4-flash"}
	endpoints := []string{"/v1/chat/completions", "/v1/responses"}
	keyHashes := []string{"key_hash_alpha", "key_hash_beta", "key_hash_gamma"}
	ipHashes := []string{"ip_hash_01", "ip_hash_02"}

	rng := rand.New(rand.NewSource(now.UnixNano()))
	prefix := fmt.Sprintf("seed_%x_", now.UnixMilli())

	// Generate ~220 records spread across the last 48 hours.
	for i := 0; i < 220; i++ {
		offset := time.Duration(rng.Intn(48*60)) * time.Minute
		createdAt := now.Add(-offset).Format(time.RFC3339)

		endpoint := endpoints[rng.Intn(len(endpoints))]
		model := models[rng.Intn(len(models))]
		keyHash := keyHashes[rng.Intn(len(keyHashes))]
		ipHash := ipHashes[rng.Intn(len(ipHashes))]

		status := 200
		roll := rng.Intn(100)
		if roll < 5 {
			status = 500
		} else if roll < 12 {
			status = 429
		} else if roll < 17 {
			status = 400
		}

		latency := int64(80 + rng.Intn(5000))
		ttfb := int64(20 + rng.Intn(int(latency/2+1)))

		inputTokens := int64(50 + rng.Intn(8000))
		outputTokens := int64(20 + rng.Intn(4000))
		totalTokens := inputTokens + outputTokens

		var reasoningTokens int64
		var cachedTokens int64

		if model == "claude-sonnet-4-6" && rng.Intn(3) == 0 {
			reasoningTokens = int64(float64(outputTokens) * (0.1 + rng.Float64()*0.5))
		}
		if rng.Intn(4) == 0 {
			cachedTokens = int64(float64(inputTokens) * (0.1 + rng.Float64()*0.7))
		}

		requestBytes := int64(200 + rng.Intn(50000))
		responseBytes := int64(100 + rng.Intn(80000))

		errMsg := ""
		captureOutcome := demoOutcomeCaptured
		if status >= 400 {
			errType := []string{"upstream_error", "rate_limited", "bad_request"}[rng.Intn(3)]
			errMsg = errType
			captureOutcome = demoOutcomeFailed
		}

		records = append(records, UsageRecord{
			CreatedAt:       createdAt,
			RequestID:       fmt.Sprintf("%s%06d", prefix, i),
			Endpoint:        endpoint,
			Method:          "POST",
			Status:          status,
			LatencyMs:       latency,
			TTFBMs:          ttfb,
			Stream:          rng.Intn(2) == 1,
			ClientIPHash:    ipHash,
			APIKeyHash:      keyHash,
			ModelRequested:  model,
			ModelReturned:   model,
			InputTokens:     inputTokens,
			OutputTokens:    outputTokens,
			ReasoningTokens: reasoningTokens,
			CachedTokens:    cachedTokens,
			TotalTokens:     totalTokens,
			RequestBytes:    requestBytes,
			ResponseBytes:   responseBytes,
			Error:           errMsg,
			EndpointProfile: endpoint,
			CaptureMode:     demoCaptureUsageMetered,
			CaptureOutcome:  captureOutcome,
			MeteringKind:    demoMeteringLLMTokens,
		})
	}

	return database.InsertBatch(records)
}
