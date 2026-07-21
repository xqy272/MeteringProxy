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
			PathMatcher:    isChatCompletionsPath,
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
			Name:           "openai_completions",
			Method:         http.MethodPost,
			PathPrefix:     "/v1/completions",
			PathMatcher:    isOpenAICompletionsPath,
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
			PathMatcher:    isResponsesPath,
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
			PathMatcher:    isAnthropicMessagesPath,
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
			Name:           "openai_images_generations",
			Method:         http.MethodPost,
			PathPrefix:     "/v1/images/generations",
			PathMatcher:    isOpenAIImagesGenerationsPath,
			CaptureMode:    event.CaptureUsageMetered,
			MeteringKind:   event.MeteringImageTokens,
			StreamProtocol: streamproto.OpenAISSE(),
			NonStreamExtractor: func(body []byte, endpoint string) (*extractor.UsageInfo, error) {
				return extractor.ExtractImageNonStreaming(body)
			},
			StreamExtractor: func(data []byte) (*extractor.UsageInfo, error) {
				return extractor.ExtractImageUsage(data)
			},
		},
		{
			Name:           "openai_images_edits",
			Method:         http.MethodPost,
			PathPrefix:     "/v1/images/edits",
			PathMatcher:    isOpenAIImagesEditsPath,
			CaptureMode:    event.CaptureUsageMetered,
			MeteringKind:   event.MeteringImageTokens,
			StreamProtocol: streamproto.OpenAISSE(),
			NonStreamExtractor: func(body []byte, endpoint string) (*extractor.UsageInfo, error) {
				return extractor.ExtractImageNonStreaming(body)
			},
			StreamExtractor: func(data []byte) (*extractor.UsageInfo, error) {
				return extractor.ExtractImageUsage(data)
			},
		},
		{
			Name:           "openai_images_variations",
			Method:         http.MethodPost,
			PathPrefix:     "/v1/images/variations",
			PathMatcher:    isOpenAIImagesVariationsPath,
			CaptureMode:    event.CaptureRequestOnly,
			MeteringKind:   event.MeteringRequestOnly,
			StreamProtocol: streamproto.None(),
		},
		{
			Name:           "openai_embeddings",
			Method:         http.MethodPost,
			PathPrefix:     "/v1/embeddings",
			PathMatcher:    isOpenAIEmbeddingsPath,
			CaptureMode:    event.CaptureUsageMetered,
			MeteringKind:   event.MeteringEmbeddingTokens,
			StreamProtocol: streamproto.None(),
			NonStreamExtractor: func(body []byte, endpoint string) (*extractor.UsageInfo, error) {
				return extractor.ExtractEmbeddingNonStreaming(body)
			},
		},
		{
			Name:           "openai_audio",
			Method:         http.MethodPost,
			PathPrefix:     "/v1/audio/{speech|transcriptions|translations}",
			PathMatcher:    isOpenAIAudioPath,
			CaptureMode:    event.CaptureRequestOnly,
			MeteringKind:   event.MeteringRequestOnly,
			StreamProtocol: streamproto.None(),
		},
		{
			Name:           "openai_videos",
			Method:         "",
			PathPrefix:     "/v1/videos",
			PathMatcher:    isOpenAIVideosPath,
			CaptureMode:    event.CaptureRequestOnly,
			MeteringKind:   event.MeteringRequestOnly,
			StreamProtocol: streamproto.None(),
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
		return geminiActionMatches(path[len(prefix):])
	}
	for _, version := range []string{"v1", "v1beta"} {
		route, ok := providerVersionRoute(path, version)
		if !ok || !strings.HasPrefix(route, "models/") {
			continue
		}
		return geminiActionMatches(route[len("models/"):])
	}
	return false
}

func isChatCompletionsPath(path string) bool {
	return path == "/v1/chat/completions" ||
		matchProviderRoute(path, "", "chat/completions") ||
		matchProviderRoute(path, "v1", "chat/completions")
}

func isOpenAICompletionsPath(path string) bool {
	return path == "/v1/completions" ||
		matchProviderRoute(path, "", "completions") ||
		matchProviderRoute(path, "v1", "completions")
}

func isResponsesPath(path string) bool {
	switch path {
	case "/v1/responses", "/v1/responses/compact", "/backend-api/codex/responses", "/backend-api/codex/responses/compact":
		return true
	default:
		return matchProviderRoute(path, "", "responses") ||
			matchProviderRoute(path, "v1", "responses")
	}
}

func isAnthropicMessagesPath(path string) bool {
	return path == "/v1/messages" || matchProviderRoute(path, "v1", "messages")
}

func isOpenAIImagesGenerationsPath(path string) bool {
	return path == "/v1/images/generations" ||
		matchProviderRoute(path, "", "images/generations") ||
		matchProviderRoute(path, "v1", "images/generations")
}

func isOpenAIImagesEditsPath(path string) bool {
	return path == "/v1/images/edits" ||
		matchProviderRoute(path, "", "images/edits") ||
		matchProviderRoute(path, "v1", "images/edits")
}

func isOpenAIImagesVariationsPath(path string) bool {
	return path == "/v1/images/variations" ||
		matchProviderRoute(path, "", "images/variations") ||
		matchProviderRoute(path, "v1", "images/variations")
}

func isOpenAIEmbeddingsPath(path string) bool {
	return path == "/v1/embeddings" ||
		matchProviderRoute(path, "", "embeddings") ||
		matchProviderRoute(path, "v1", "embeddings")
}

func isOpenAIAudioPath(path string) bool {
	switch path {
	case "/v1/audio/speech", "/v1/audio/transcriptions", "/v1/audio/translations":
		return true
	default:
		return matchProviderRoute(path, "v1", "audio/speech") ||
			matchProviderRoute(path, "v1", "audio/transcriptions") ||
			matchProviderRoute(path, "v1", "audio/translations")
	}
}

func isOpenAIVideosPath(path string) bool {
	if path == "/v1/videos" || path == "/v1/videos/edits" ||
		strings.HasPrefix(path, "/v1/videos/") {
		return true
	}
	return matchProviderRoute(path, "v1", "videos") ||
		matchProviderRoute(path, "v1", "videos/edits") ||
		matchProviderRoutePrefix(path, "v1", "videos/")
}

func matchProviderRoutePrefix(path, version, prefix string) bool {
	route, ok := providerVersionRoute(path, version)
	return ok && strings.HasPrefix(route, prefix)
}

func matchProviderRoute(path, version, suffix string) bool {
	route := ""
	var ok bool
	if version == "" {
		route, ok = providerRouteWithoutVersion(path)
	} else {
		route, ok = providerVersionRoute(path, version)
	}
	return ok && route == suffix
}

func providerRouteWithoutVersion(path string) (string, bool) {
	rest, ok := providerRouteRest(path)
	if !ok {
		return "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", false
	}
	return parts[1], true
}

func providerVersionRoute(path, version string) (string, bool) {
	route, ok := providerRouteWithoutVersion(path)
	if !ok {
		return "", false
	}
	prefix := version + "/"
	if !strings.HasPrefix(route, prefix) {
		return "", false
	}
	return strings.TrimPrefix(route, prefix), true
}

func providerRouteRest(path string) (string, bool) {
	const prefix = "/api/provider/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" || strings.HasPrefix(rest, "/") {
		return "", false
	}
	return rest, true
}

func geminiActionMatches(rest string) bool {
	colon := strings.LastIndexByte(rest, ':')
	if colon <= 0 || colon == len(rest)-1 {
		return false
	}
	action := rest[colon+1:]
	return action == "generateContent" || action == "streamGenerateContent"
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

// EndpointMetas returns metadata API endpoint descriptors for all profiles.
func (r *Registry) EndpointMetas() []EndpointMeta {
	out := make([]EndpointMeta, 0, len(r.profiles))
	for _, p := range r.profiles {
		out = append(out, p.ToEndpointMeta())
	}
	return out
}

// GatewayProfiles returns static capability-matrix entries for gateway reports.
func (r *Registry) GatewayProfiles() []GatewayProfileInfo {
	out := make([]GatewayProfileInfo, 0, len(r.profiles))
	for _, p := range r.profiles {
		out = append(out, p.GatewayProfileInfo())
	}
	return out
}
