package profile

import (
	"fmt"
	"net/http"
	"strings"

	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/extractor"
	"ai-gateway-metering-proxy/internal/streamproto"
)

// Registry holds all active endpoint profiles and dispatches requests.
type Registry struct {
	profiles []*EndpointProfile
}

// NewRegistry creates a registry with the built-in profiles and applies
// any config-driven overrides. This is the bootstrap entry point.
func NewRegistry() *Registry {
	r := &Registry{}
	r.registerBuiltins()
	return r
}

func (r *Registry) registerBuiltins() {
	r.profiles = []*EndpointProfile{
		{
			Name:           "chat_completions",
			Method:         http.MethodPost,
			PathPrefix:     "/v1/chat/completions",
			CaptureMode:    event.CaptureUsageMetered,
			MeteringKind:   event.MeteringLLMTokens,
			StreamProtocol: streamproto.OpenAISSE(),
			NonStreamExtractor: func(body []byte, endpoint string) (*extractor.UsageInfo, error) {
				return extractor.ExtractNonStreaming(body, endpoint)
			},
			StreamExtractor: func(data []byte) (*extractor.UsageInfo, error) {
				return extractor.ExtractChatUsage(data)
			},
		},
		{
			Name:           "responses",
			Method:         http.MethodPost,
			PathPrefix:     "/v1/responses",
			CaptureMode:    event.CaptureUsageMetered,
			MeteringKind:   event.MeteringLLMTokens,
			StreamProtocol: streamproto.OpenAISSE(),
			NonStreamExtractor: func(body []byte, endpoint string) (*extractor.UsageInfo, error) {
				return extractor.ExtractNonStreaming(body, endpoint)
			},
			StreamExtractor: func(data []byte) (*extractor.UsageInfo, error) {
				return extractor.ExtractResponsesUsage(data)
			},
		},
		{
			Name:           "anthropic_messages",
			Method:         http.MethodPost,
			PathPrefix:     "/v1/messages",
			CaptureMode:    event.CaptureUsageMetered,
			MeteringKind:   event.MeteringLLMTokens,
			StreamProtocol: streamproto.OpenAISSE(),
			NonStreamExtractor: func(body []byte, endpoint string) (*extractor.UsageInfo, error) {
				return extractor.ExtractAnthropicNonStreaming(body)
			},
			StreamExtractor: func(data []byte) (*extractor.UsageInfo, error) {
				return extractor.ExtractAnthropicUsage(data)
			},
		},
		{
			Name:           "gemini_generate_content",
			Method:         http.MethodPost,
			PathPrefix:     "/v1(beta)?/models/{model}:generateContent|streamGenerateContent",
			PathMatcher:    isGeminiGenerateContentPath,
			CaptureMode:    event.CaptureUsageMetered,
			MeteringKind:   event.MeteringLLMTokens,
			StreamProtocol: streamproto.OpenAISSE(),
			NonStreamExtractor: func(body []byte, endpoint string) (*extractor.UsageInfo, error) {
				return extractor.ExtractGeminiNonStreaming(body)
			},
			StreamExtractor: func(data []byte) (*extractor.UsageInfo, error) {
				return extractor.ExtractGeminiUsage(data)
			},
		},
		{
			Name:           "unknown_passthrough",
			Method:         "", // matches any method
			PathPrefix:     "", // matches any path
			CaptureMode:    event.CapturePassthrough,
			MeteringKind:   event.MeteringNone,
			StreamProtocol: streamproto.None(),
		},
	}
}

func isGeminiGenerateContentPath(path string) bool {
	for _, prefix := range []string{"/v1/models/", "/v1beta/models/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := path[len(prefix):]
		colon := strings.LastIndexByte(rest, ':')
		if colon <= 0 || colon == len(rest)-1 {
			return false
		}
		action := rest[colon+1:]
		return action == "generateContent" || action == "streamGenerateContent"
	}
	return false
}

// Match finds the first profile matching the method and path.
// The unknown_passthrough profile (empty method/path) is always last and
// matches everything, serving as the catch-all.
func (r *Registry) Match(method, path string) (*EndpointProfile, error) {
	for _, p := range r.profiles {
		if p.Name == "unknown_passthrough" {
			continue // handled as fallback
		}
		if p.Match(method, path) {
			return p, nil
		}
	}
	// Return the catch-all passthrough profile.
	for _, p := range r.profiles {
		if p.Name == "unknown_passthrough" {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no profile matched and no passthrough fallback registered")
}

// Profiles returns all registered profiles (for metadata API).
func (r *Registry) Profiles() []*EndpointProfile {
	return r.profiles
}

// MeteredProfiles returns only metered profiles (for metadata API filter lists).
func (r *Registry) MeteredProfiles() []*EndpointProfile {
	var result []*EndpointProfile
	for _, p := range r.profiles {
		if p.IsMetered() {
			result = append(result, p)
		}
	}
	return result
}
