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
	demoCaptureRequestOnly  = "request_only"
	demoOutcomeCaptured     = "captured"
	demoOutcomeFailed       = "failed"
	demoMeteringLLMTokens   = "llm_tokens"
	demoMeteringImageTokens = "image_tokens"
	demoMeteringRequestOnly = "request_only"

	demoKeyHashFriendA   = "1111111111111111111111111111111111111111111111111111111111111111"
	demoKeyHashFriendB   = "2222222222222222222222222222222222222222222222222222222222222222"
	demoKeyHashUnlabeled = "3333333333333333333333333333333333333333333333333333333333333333"
	demoKeyHashPartial   = "4444444444444444444444444444444444444444444444444444444444444444"
	demoIPHashA          = "5555555555555555555555555555555555555555555555555555555555555555"
	demoIPHashB          = "6666666666666666666666666666666666666666666666666666666666666666"
)

// DemoKeyLabels returns the synthetic labels used by --seed-demo. The caller
// may merge these into the in-memory config only for that explicitly guarded
// development mode; labels are never persisted to SQLite.
func DemoKeyLabels() map[string]string {
	return map[string]string{
		demoKeyHashFriendA: "friend-a",
		demoKeyHashFriendB: "agent-lab",
	}
}

func SeedDemo(database *DB) error {
	if err := clearDemoData(database); err != nil {
		return err
	}

	now := time.Now().UTC()
	var records []UsageRecord

	// Models that match pricing.yaml so cost coverage is meaningful.
	models := []string{"gpt-5.4", "gpt-5.4-mini", "claude-sonnet-4-6", "claude-haiku-4-5", "deepseek-v4-pro", "deepseek-v4-flash"}
	endpoints := []string{"/v1/chat/completions", "/v1/responses"}
	keyHashes := []string{demoKeyHashFriendA, demoKeyHashFriendB, demoKeyHashUnlabeled}
	ipHashes := []string{demoIPHashA, demoIPHashB}

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
		var errClass, errType, errCode string
		if status >= 400 {
			errClasses := []string{"quota_exhausted", "rate_limited", "auth_failed", "upstream_5xx", "invalid_request", "context_length", "proxy_upstream_error"}
			errClass = errClasses[rng.Intn(len(errClasses))]
			switch errClass {
			case "quota_exhausted":
				errType = "insufficient_quota"
				errCode = "insufficient_quota"
			case "rate_limited":
				errType = "rate_limit_error"
				errCode = "rate_limit_exceeded"
			case "auth_failed":
				errType = "invalid_request_error"
				errCode = "invalid_api_key"
			case "upstream_5xx":
				errType = "server_error"
				errCode = "internal_error"
			case "invalid_request":
				errType = "invalid_request_error"
				errCode = "invalid_request"
			case "context_length":
				errType = "invalid_request_error"
				errCode = "context_length_exceeded"
			case "proxy_upstream_error":
				errClass = "proxy_connection_refused"
				errType = "proxy_transport"
				errCode = "connection_refused"
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
		})
	}

	records = append(records, demoCoverageRecords(now, prefix)...)
	if err := database.InsertBatch(records); err != nil {
		return err
	}
	return seedCredentialQuotaDemo(database, now)
}

func demoCoverageRecords(now time.Time, prefix string) []UsageRecord {
	at := func(delta time.Duration) string {
		return now.Add(delta).Format(time.RFC3339)
	}
	base := func(suffix, createdAt, keyHash, endpoint, profile, model string) UsageRecord {
		return UsageRecord{
			CreatedAt:           createdAt,
			RequestID:           prefix + suffix,
			Endpoint:            endpoint,
			Method:              "POST",
			Status:              200,
			LatencyMs:           720,
			TTFBMs:              180,
			ClientIPHash:        demoIPHashA,
			APIKeyHash:          keyHash,
			ModelRequested:      model,
			ModelReturned:       model,
			EndpointProfile:     profile,
			CaptureMode:         demoCaptureUsageMetered,
			CaptureOutcome:      demoOutcomeCaptured,
			MeteringKind:        demoMeteringLLMTokens,
			ModelReturnedSource: "response_body",
			UsageSource:         "http_response",
		}
	}

	longContext := base(
		"long_context",
		at(-5*time.Minute),
		demoKeyHashFriendA,
		"/v1beta/models/gemini-3.1-pro-preview:generateContent",
		"gemini_generate_content",
		"gemini-3.1-pro-preview",
	)
	longContext.InputTokens = 200000
	longContext.CachedTokens = 50000
	longContext.OutputTokens = 5000
	longContext.TotalTokens = 205000

	image1K := base(
		"image_1k",
		at(-4*time.Minute),
		demoKeyHashFriendB,
		"/v1/images/generations",
		"openai_images_generations",
		"grok-imagine-image-quality",
	)
	image1K.MeteringKind = demoMeteringImageTokens
	image1K.ImageUsage = &ImageUsageRecord{
		Operation:       "generation",
		Provider:        "xai",
		Size:            "1024x1024",
		ImageCount:      2,
		InputImageCount: 1,
	}

	image2K := base(
		"image_2k",
		at(-3*time.Minute),
		demoKeyHashUnlabeled,
		"/v1/images/edits",
		"openai_images_edits",
		"grok-imagine-image-quality",
	)
	image2K.MeteringKind = demoMeteringImageTokens
	image2K.ImageUsage = &ImageUsageRecord{
		Operation:       "edit",
		Provider:        "xai",
		Size:            "2048x2048",
		ImageCount:      1,
		InputImageCount: 2,
		HasMask:         true,
	}

	unknownKey := base(
		"unknown_key",
		at(-2*time.Minute),
		"",
		"/v1/responses",
		"responses",
		"gpt-5.4-mini",
	)
	unknownKey.InputTokens = 1200
	unknownKey.OutputTokens = 300
	unknownKey.TotalTokens = 1500

	partialUnpriced := base(
		"partial_unpriced",
		at(-time.Minute),
		demoKeyHashPartial,
		"/v1/chat/completions",
		"chat_completions",
		"unpriced-model-v1",
	)
	partialUnpriced.Status = 429
	partialUnpriced.InputTokens = 900
	partialUnpriced.OutputTokens = 100
	partialUnpriced.TotalTokens = 1000
	partialUnpriced.Error = "rate_limited"
	partialUnpriced.ErrorClass = "rate_limited"
	partialUnpriced.ErrorType = "rate_limit_error"
	partialUnpriced.ErrorCode = "rate_limit_exceeded"

	partialRequestOnly := base(
		"partial_request_only",
		at(-30*time.Second),
		demoKeyHashPartial,
		"/v1/audio/speech",
		"openai_audio",
		"voice-demo",
	)
	partialRequestOnly.CaptureMode = demoCaptureRequestOnly
	partialRequestOnly.MeteringKind = demoMeteringRequestOnly
	partialRequestOnly.CaptureReason = "request_only_profile"
	partialRequestOnly.ModelReturned = ""
	partialRequestOnly.ModelReturnedSource = ""
	partialRequestOnly.UsageSource = ""

	return []UsageRecord{
		longContext,
		image1K,
		image2K,
		unknownKey,
		partialUnpriced,
		partialRequestOnly,
	}
}

func clearDemoData(database *DB) error {
	if _, err := database.sql.Exec(`
		DELETE FROM usage_dimensions
		WHERE request_usage_id IN (SELECT id FROM request_usage WHERE request_id LIKE 'seed_%')
	`); err != nil {
		return err
	}
	if _, err := database.sql.Exec(`
		DELETE FROM image_usage
		WHERE request_usage_id IN (SELECT id FROM request_usage WHERE request_id LIKE 'seed_%')
	`); err != nil {
		return err
	}
	if _, err := database.sql.Exec(`DELETE FROM request_usage WHERE request_id LIKE 'seed_%'`); err != nil {
		return err
	}
	if _, err := database.sql.Exec(`DELETE FROM credential_health WHERE credential_hash LIKE 'demo_cred_%'`); err != nil {
		return err
	}
	if _, err := database.sql.Exec(`DELETE FROM quota_current WHERE credential_hash LIKE 'demo_cred_%'`); err != nil {
		return err
	}
	if _, err := database.sql.Exec(`DELETE FROM quota_refresh_events WHERE credential_hash LIKE 'demo_cred_%'`); err != nil {
		return err
	}
	return nil
}

func seedCredentialQuotaDemo(database *DB, now time.Time) error {
	checkedAt := now.Format(time.RFC3339)
	checkedAtUnix := now.Unix()
	fiveHourReset := now.Add(2*time.Hour + 17*time.Minute)
	fiveHourResetStr := fiveHourReset.Format(time.RFC3339)
	fiveHourResetUnix := fiveHourReset.Unix()
	weeklyReset := now.Add(4*24*time.Hour + 6*time.Hour)
	weeklyResetStr := weeklyReset.Format(time.RFC3339)
	weeklyResetUnix := weeklyReset.Unix()
	makeRecent := func() []CredentialRecentRequestBucket {
		rows := make([]CredentialRecentRequestBucket, 20)
		start := now.Add(-200 * time.Minute)
		for i := range rows {
			bucketStart := start.Add(time.Duration(i) * 10 * time.Minute)
			bucketEnd := bucketStart.Add(10 * time.Minute)
			rows[i].Time = bucketStart.Format("15:04") + " - " + bucketEnd.Format("15:04")
		}
		return rows
	}
	sumRecent := func(rows []CredentialRecentRequestBucket) (int64, int64) {
		var success, failed int64
		for _, row := range rows {
			success += row.Success
			failed += row.Failed
		}
		return success, failed
	}
	codexPrimaryRecent := makeRecent()
	codexPrimaryRecent[17].Success = 26
	codexPrimaryRecent[18].Success = 37
	codexPrimaryRecent[18].Failed = 1
	codexPrimaryRecentSuccess, codexPrimaryRecentFailed := sumRecent(codexPrimaryRecent)
	codexSpareRecent := makeRecent()
	codexSpareRecent[15].Success = 18
	codexSpareRecent[16].Failed = 8
	codexSpareRecent[17].Failed = 11
	codexSpareRecentSuccess, codexSpareRecentFailed := sumRecent(codexSpareRecent)
	claudeRecent := makeRecent()
	claudeRecent[18].Success = 40
	claudeRecent[18].Failed = 1
	claudeRecentSuccess, claudeRecentFailed := sumRecent(claudeRecent)
	antigravityRecent := makeRecent()
	antigravityRecent[16].Success = 31
	antigravityRecent[17].Success = 24
	antigravityRecent[18].Success = 18
	antigravityRecent[18].Failed = 4
	antigravityRecentSuccess, antigravityRecentFailed := sumRecent(antigravityRecent)

	credentials := []CredentialHealthRow{
		{
			Provider:           "codex",
			CredentialHash:     "demo_cred_codex_primary",
			AuthIndexHash:      "demo_auth_codex_0",
			LabelHash:          "demo_label_codex_primary",
			DisplayLabel:       "Codex Primary",
			IdentityHint:       "codex-primary@example.com",
			Status:             "ready",
			Plan:               "plus",
			SuccessCount:       184,
			FailedCount:        3,
			RecentSuccessCount: codexPrimaryRecentSuccess,
			RecentFailedCount:  codexPrimaryRecentFailed,
			RecentRequests:     codexPrimaryRecent,
			CheckedAt:          checkedAt,
			CheckedAtUnix:      checkedAtUnix,
		},
		{
			Provider:           "codex",
			CredentialHash:     "demo_cred_codex_spare",
			AuthIndexHash:      "demo_auth_codex_1",
			LabelHash:          "demo_label_codex_spare",
			DisplayLabel:       "Codex Spare",
			IdentityHint:       "codex-spare@example.com",
			Status:             "unavailable",
			Plan:               "plus",
			SuccessCount:       42,
			FailedCount:        19,
			RecentSuccessCount: codexSpareRecentSuccess,
			RecentFailedCount:  codexSpareRecentFailed,
			RecentRequests:     codexSpareRecent,
			CheckedAt:          checkedAt,
			CheckedAtUnix:      checkedAtUnix,
			ErrorClass:         "auth_failed",
		},
		{
			Provider:           "claude",
			CredentialHash:     "demo_cred_claude_team",
			AuthIndexHash:      "demo_auth_claude_0",
			LabelHash:          "demo_label_claude_team",
			DisplayLabel:       "Claude Team",
			IdentityHint:       "claude-team@example.com",
			Status:             "ready",
			Plan:               "max",
			SuccessCount:       91,
			FailedCount:        1,
			RecentSuccessCount: claudeRecentSuccess,
			RecentFailedCount:  claudeRecentFailed,
			RecentRequests:     claudeRecent,
			CheckedAt:          checkedAt,
			CheckedAtUnix:      checkedAtUnix,
		},
		{
			Provider:           "antigravity",
			CredentialHash:     "demo_cred_antigravity_workspace",
			AuthIndexHash:      "demo_auth_antigravity_0",
			LabelHash:          "demo_label_antigravity_workspace",
			DisplayLabel:       "Antigravity Workspace",
			IdentityHint:       "workspace@example.com",
			Status:             "ready",
			Plan:               "google-project",
			SuccessCount:       73,
			FailedCount:        4,
			RecentSuccessCount: antigravityRecentSuccess,
			RecentFailedCount:  antigravityRecentFailed,
			RecentRequests:     antigravityRecent,
			CheckedAt:          checkedAt,
			CheckedAtUnix:      checkedAtUnix,
		},
		{
			Provider:       "kimi",
			CredentialHash: "demo_cred_kimi_disabled",
			AuthIndexHash:  "demo_auth_kimi_0",
			LabelHash:      "demo_label_kimi_disabled",
			DisplayLabel:   "Kimi Disabled",
			IdentityHint:   "kimi-disabled@example.com",
			Status:         "disabled",
			SuccessCount:   0,
			FailedCount:    0,
			CheckedAt:      checkedAt,
			CheckedAtUnix:  checkedAtUnix,
			ErrorClass:     "credential_disabled",
		},
	}
	for i := range credentials {
		if err := database.UpsertCredentialHealth(&credentials[i]); err != nil {
			return err
		}
	}

	quotaRows := []QuotaCurrentRow{
		{
			Provider:        "codex",
			CredentialHash:  "demo_cred_codex_primary",
			WindowKey:       "5h",
			CheckedAt:       checkedAt,
			CheckedAtUnix:   checkedAtUnix,
			Plan:            "plus",
			LimitAmount:     180,
			RemainingAmount: 64,
			UsedAmount:      116,
			Unit:            "requests",
			ResetAt:         fiveHourResetStr,
			ResetAtUnix:     fiveHourResetUnix,
			Status:          "ok",
			QuotaSupported:  1,
			AdapterStatus:   "available",
		},
		{
			Provider:        "codex",
			CredentialHash:  "demo_cred_codex_primary",
			WindowKey:       "weekly",
			CheckedAt:       checkedAt,
			CheckedAtUnix:   checkedAtUnix,
			Plan:            "plus",
			LimitAmount:     900,
			RemainingAmount: 514,
			UsedAmount:      386,
			Unit:            "requests",
			ResetAt:         weeklyResetStr,
			ResetAtUnix:     weeklyResetUnix,
			Status:          "ok",
			QuotaSupported:  1,
			AdapterStatus:   "available",
		},
		{
			Provider:        "codex",
			CredentialHash:  "demo_cred_codex_spare",
			WindowKey:       "5h",
			CheckedAt:       checkedAt,
			CheckedAtUnix:   checkedAtUnix,
			Plan:            "plus",
			LimitAmount:     180,
			RemainingAmount: 0,
			UsedAmount:      180,
			Unit:            "requests",
			ResetAt:         fiveHourResetStr,
			ResetAtUnix:     fiveHourResetUnix,
			Status:          "exhausted",
			QuotaSupported:  1,
			AdapterStatus:   "available",
			ErrorClass:      "quota_exhausted",
		},
		{
			Provider:        "claude",
			CredentialHash:  "demo_cred_claude_team",
			WindowKey:       "5h",
			CheckedAt:       checkedAt,
			CheckedAtUnix:   checkedAtUnix,
			Plan:            "max",
			LimitAmount:     45,
			RemainingAmount: 4,
			UsedAmount:      41,
			Unit:            "credits",
			ResetAt:         fiveHourResetStr,
			ResetAtUnix:     fiveHourResetUnix,
			Status:          "low",
			QuotaSupported:  1,
			AdapterStatus:   "available",
			ErrorClass:      "quota_low",
		},
		{
			Provider:        "antigravity",
			CredentialHash:  "demo_cred_antigravity_workspace",
			WindowKey:       "project_daily",
			CheckedAt:       checkedAt,
			CheckedAtUnix:   checkedAtUnix,
			Plan:            "google-project",
			LimitAmount:     1000,
			RemainingAmount: 720,
			UsedAmount:      280,
			Unit:            "requests",
			ResetAt:         now.Add(11 * time.Hour).Format(time.RFC3339),
			ResetAtUnix:     now.Add(11 * time.Hour).Unix(),
			Status:          "ok",
			QuotaSupported:  1,
			AdapterStatus:   "available",
		},
		{
			Provider:        "antigravity",
			CredentialHash:  "demo_cred_antigravity_workspace",
			WindowKey:       "model_rate",
			CheckedAt:       checkedAt,
			CheckedAtUnix:   checkedAtUnix,
			Plan:            "google-project",
			LimitAmount:     60,
			RemainingAmount: 42,
			UsedAmount:      18,
			Unit:            "rpm",
			ResetAt:         now.Add(42 * time.Minute).Format(time.RFC3339),
			ResetAtUnix:     now.Add(42 * time.Minute).Unix(),
			Status:          "ok",
			QuotaSupported:  1,
			AdapterStatus:   "available",
		},
		{
			Provider:        "claude",
			CredentialHash:  "demo_cred_claude_team",
			WindowKey:       "weekly",
			CheckedAt:       checkedAt,
			CheckedAtUnix:   checkedAtUnix,
			Plan:            "max",
			LimitAmount:     250,
			RemainingAmount: 188,
			UsedAmount:      62,
			Unit:            "credits",
			ResetAt:         weeklyResetStr,
			ResetAtUnix:     weeklyResetUnix,
			Status:          "ok",
			QuotaSupported:  1,
			AdapterStatus:   "available",
		},
		{
			Provider:       "kimi",
			CredentialHash: "demo_cred_kimi_disabled",
			WindowKey:      "weekly",
			CheckedAt:      checkedAt,
			CheckedAtUnix:  checkedAtUnix,
			Status:         "unsupported",
			QuotaSupported: 0,
			AdapterStatus:  "unsupported",
			ErrorClass:     "quota_unsupported",
			Partial:        1,
		},
	}
	for i := range quotaRows {
		if err := database.UpsertQuotaCurrent(&quotaRows[i]); err != nil {
			return err
		}
	}

	return database.InsertQuotaRefreshEvent(&QuotaRefreshEventRow{
		CheckedAt:         checkedAt,
		CheckedAtUnix:     checkedAtUnix,
		Provider:          "kimi",
		CredentialHash:    "demo_cred_kimi_disabled",
		Phase:             "provider_adapter",
		Status:            "error",
		AdapterStatus:     "unsupported",
		DurationMs:        18,
		ErrorClass:        "quota_unsupported",
		Partial:           1,
		ProbeHTTPStatus:   400,
		ProbeEndpoint:     "/api-call",
		ProbeErrorClass:   "api_call_bad_request",
		APICallReachable:  1,
		ProviderSupported: 0,
		DetailsJSON:       `{"probe_status":"api_call_bad_request","event":"unsupported"}`,
	})
}
