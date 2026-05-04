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
		var errClass, errType, errCode, errMessage string
		if status >= 400 {
			errClasses := []string{"quota_exhausted", "rate_limited", "auth_failed", "upstream_5xx", "invalid_request", "context_length", "proxy_upstream_error"}
			errClass = errClasses[rng.Intn(len(errClasses))]
			switch errClass {
			case "quota_exhausted":
				errType = "insufficient_quota"
				errCode = "insufficient_quota"
				errMessage = "You exceeded your current quota, please check your plan and billing details."
			case "rate_limited":
				errType = "rate_limit_error"
				errCode = "rate_limit_exceeded"
				errMessage = "Rate limit reached for requests to this model."
			case "auth_failed":
				errType = "invalid_request_error"
				errCode = "invalid_api_key"
				errMessage = "Incorrect API key provided."
			case "upstream_5xx":
				errType = "server_error"
				errCode = "internal_error"
				errMessage = "The server had an error processing your request."
			case "invalid_request":
				errType = "invalid_request_error"
				errCode = "invalid_request"
				errMessage = "Invalid request parameters."
			case "context_length":
				errType = "invalid_request_error"
				errCode = "context_length_exceeded"
				errMessage = "This model's maximum context length is 128000 tokens."
			case "proxy_upstream_error":
				errType = ""
				errCode = ""
				errMessage = "dial tcp: connection refused"
			}
			errMsg = errClass
			captureOutcome = demoOutcomeFailed
		}

		// ~10% of records have model_returned empty (fallback to requested)
		var modelReturned string = model
		if rng.Intn(10) == 0 {
			modelReturned = ""
		}

		// Add an unpriced model for ~5 models
		if rng.Intn(20) == 0 {
			model = "unpriced-model-v1"
			modelReturned = "unpriced-model-v1"
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
			ModelReturned:   modelReturned,
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
			ErrorClass:      errClass,
			ErrorType:       errType,
			ErrorCode:       errCode,
			ErrorMessage:    errMessage,
		})
	}

	return database.InsertBatch(records)
}
