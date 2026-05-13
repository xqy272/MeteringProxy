package usagequeue

import (
	"testing"

	"ai-gateway-metering-proxy/internal/config"
	"ai-gateway-metering-proxy/internal/hash"
)

func TestParsePayloadHashesSideChannelFieldsWithNamespace(t *testing.T) {
	h := hash.NewWithSalt("test-salt")
	p := NewPoller("", "", config.UsageQueueConfig{}, nil, h, false)

	event := p.parsePayload([]byte(`{
		"request_id":"req-1",
		"api_key":"same-secret",
		"source":"auth-file",
		"auth_index":2,
		"tokens":{
			"input_tokens":3,
			"output_tokens":4,
			"reasoning_tokens":5
		}
	}`))

	if event.APIKeyHash != h.Hash("side_usage:api_key:raw:same-secret") {
		t.Fatalf("APIKeyHash = %q, want namespaced side-channel hash", event.APIKeyHash)
	}
	if event.APIKeyHash == h.Hash("same-secret") {
		t.Fatal("APIKeyHash used unnamespaced request hash")
	}
	if event.SourceHash != h.Hash("side_usage:source:raw:auth-file") {
		t.Fatalf("SourceHash = %q", event.SourceHash)
	}
	if event.AuthIndexHash != h.Hash("side_usage:auth_index:raw:2") {
		t.Fatalf("AuthIndexHash = %q", event.AuthIndexHash)
	}
	if event.InputTokens != 3 || event.OutputTokens != 4 || event.ReasoningTokens != 5 {
		t.Fatalf("tokens = input %d output %d reasoning %d", event.InputTokens, event.OutputTokens, event.ReasoningTokens)
	}
	if event.TotalTokens != 12 {
		t.Fatalf("TotalTokens = %d, want derived 12", event.TotalTokens)
	}
}

func TestParsePayloadMarksInvalidJSON(t *testing.T) {
	p := NewPoller("", "", config.UsageQueueConfig{}, nil, hash.NewWithSalt("test-salt"), false)
	event := p.parsePayload([]byte(`{bad json}`))
	if event.MatchStatus != "invalid_payload" || event.ErrorClass != "invalid_payload" {
		t.Fatalf("event = %#v", event)
	}
}

func TestParsePayloadKeepsCLIProxyAPIv704CacheTokenBreakdown(t *testing.T) {
	p := NewPoller("", "", config.UsageQueueConfig{}, nil, hash.NewWithSalt("test-salt"), false)
	event := p.parsePayload([]byte(`{
		"request_id":"req-2",
		"tokens":{
			"input_tokens":100,
			"output_tokens":25,
			"reasoning_tokens":5,
			"cache_read_tokens":40,
			"cache_creation_tokens":7,
			"total_tokens":130
		}
	}`))
	if event.CachedTokens != 40 {
		t.Fatalf("CachedTokens = %d, want cache_read_tokens fallback 40", event.CachedTokens)
	}
	if event.CacheReadTokens != 40 || event.CacheCreationTokens != 7 {
		t.Fatalf("cache breakdown = read %d creation %d", event.CacheReadTokens, event.CacheCreationTokens)
	}
	if event.TotalTokens != 130 {
		t.Fatalf("TotalTokens = %d, want 130", event.TotalTokens)
	}
}
