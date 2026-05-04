package profile

import (
	"strings"

	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/extractor"
	"ai-gateway-metering-proxy/internal/streamproto"
)

// EndpointProfile describes an endpoint the proxy can handle.
type EndpointProfile struct {
	Name           string
	Method         string
	PathPrefix     string // exact URL.Path match for built-in metered profiles
	PathMatcher    func(path string) bool
	CaptureMode    string
	MeteringKind   string
	StreamProtocol streamproto.Protocol

	// Extractors are bound to the profile.
	NonStreamExtractor func(body []byte, endpoint string) (*extractor.UsageInfo, error)
	StreamExtractor    func(data []byte) (*extractor.UsageInfo, error)
}

// Match returns true if this profile matches the given method and path.
func (p *EndpointProfile) Match(method, path string) bool {
	if p.Method != "" && p.Method != method {
		return false
	}
	if p.PathMatcher != nil {
		return p.PathMatcher(path)
	}
	path = strings.TrimRight(path, "/")
	return path == p.PathPrefix
}

// DisplayName returns a human-readable name for UI display.
func (p *EndpointProfile) DisplayName() string {
	switch p.Name {
	case "chat_completions":
		return "Chat Completions"
	case "responses":
		return "Responses API"
	case "anthropic_messages":
		return "Anthropic Messages"
	case "gemini_generate_content":
		return "Gemini Generate Content"
	case "unknown_passthrough":
		return "Unknown (Passthrough)"
	default:
		return p.Name
	}
}

// IsMetered returns true if this profile should be metered (not passthrough).
func (p *EndpointProfile) IsMetered() bool {
	return p.CaptureMode != event.CapturePassthrough
}

// ToEndpointMeta converts this profile to an event.EndpointMeta for metadata API.
func (p *EndpointProfile) ToEndpointMeta() event.EndpointMeta {
	filterValue := p.PathPrefix
	if p.PathMatcher != nil {
		filterValue = "profile:" + p.Name
	}
	return event.EndpointMeta{
		Name:         p.Name,
		Path:         p.PathPrefix,
		FilterValue:  filterValue,
		Method:       p.Method,
		DisplayName:  p.DisplayName(),
		MeteringKind: p.MeteringKind,
		CaptureMode:  p.CaptureMode,
	}
}
