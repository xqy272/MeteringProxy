package report

import (
	"context"
	"fmt"

	"ai-gateway-metering-proxy/internal/event"
)

func (s *Service) Metadata(ctx context.Context, _ MetadataFilter) (MetadataReport, error) {
	if s == nil {
		return MetadataReport{}, fmt.Errorf("report service is not configured")
	}
	if err := ctx.Err(); err != nil {
		return MetadataReport{}, err
	}

	meta := MetadataReport{
		Endpoints: []EndpointMeta{},
		Ranges: []RangeMeta{
			{Key: "24h", Label: "Last 24 Hours", Bucket: "1h"},
			{Key: "today", Label: "Today", Bucket: "1h"},
			{Key: "7d", Label: "Last 7 Days", Bucket: "1h"},
			{Key: "30d", Label: "Last 30 Days", Bucket: "1d"},
		},
		Buckets: []BucketMeta{
			{Key: "1h", Label: "1 Hour"},
			{Key: "1d", Label: "1 Day"},
		},
		MeteringKinds: []string{
			event.MeteringLLMTokens,
			event.MeteringImageTokens,
			event.MeteringEmbeddingTokens,
			event.MeteringAudioSeconds,
			event.MeteringRequestOnly,
			event.MeteringNone,
		},
		CaptureModes: []string{
			event.CaptureUsageMetered,
			event.CapturePassthrough,
			event.CaptureRequestOnly,
		},
	}

	if s.profiles != nil {
		for _, p := range s.profiles.EndpointMetas() {
			meta.Endpoints = append(meta.Endpoints, EndpointMeta{
				Name:         p.Name,
				Path:         p.Path,
				FilterValue:  p.FilterValue,
				Method:       p.Method,
				DisplayName:  p.DisplayName,
				MeteringKind: p.MeteringKind,
				CaptureMode:  p.CaptureMode,
			})
		}
	}
	return meta, nil
}
