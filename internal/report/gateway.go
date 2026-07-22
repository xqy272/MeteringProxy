package report

import (
	"context"
	"fmt"
	"sort"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/event"
)

func (s *Service) GatewayCapabilities(ctx context.Context, filter GatewayFilter) (GatewayCapabilitiesReport, error) {
	if s == nil {
		return GatewayCapabilitiesReport{}, fmt.Errorf("report service is not configured")
	}

	dbRows, err := s.gateway.GatewayCapabilitiesReport(ctx, filter.Since)
	if err != nil {
		return GatewayCapabilitiesReport{}, err
	}

	byProfile := make(map[string]db.GatewayCapabilityRow, len(dbRows))
	for _, row := range dbRows {
		byProfile[row.EndpointProfile] = row
	}

	out := GatewayCapabilitiesReport{
		Range:    filter.Range,
		Profiles: []GatewayCapabilityProfile{},
	}

	seen := make(map[string]bool)
	unknownFolded := false
	if s.profiles != nil {
		for _, p := range s.profiles.GatewayProfiles() {
			seen[p.Name] = true
			row := byProfile[p.Name]
			// unknown_passthrough also absorbs empty endpoint_profile traffic ("unknown").
			if p.Name == "unknown_passthrough" {
				unknownFolded = true
				if extra, ok := byProfile["unknown"]; ok {
					row.RequestCount += extra.RequestCount
					row.StreamCount += extra.StreamCount
					row.MissingUsageCount += extra.MissingUsageCount
					row.UsageMeteredCount += extra.UsageMeteredCount
					row.RequestOnlyCount += extra.RequestOnlyCount
					row.PassthroughCount += extra.PassthroughCount
				}
			}

			out.Profiles = append(out.Profiles, GatewayCapabilityProfile{
				Name:              p.Name,
				DisplayName:       p.DisplayName,
				CaptureMode:       p.CaptureMode,
				MeteringKind:      p.MeteringKind,
				RequestCount:      row.RequestCount,
				MissingUsageCount: row.MissingUsageCount,
				StreamCount:       row.StreamCount,
				KnownLimitations:  p.KnownLimitations,
			})
			addGatewaySummary(&out.Summary, row)
		}
	}

	extraNames := make([]string, 0)
	for name := range byProfile {
		if seen[name] || name == "unknown" && unknownFolded {
			continue
		}
		extraNames = append(extraNames, name)
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		row := byProfile[name]
		out.Profiles = append(out.Profiles, GatewayCapabilityProfile{
			Name:              name,
			DisplayName:       name,
			CaptureMode:       event.CapturePassthrough,
			RequestCount:      row.RequestCount,
			MissingUsageCount: row.MissingUsageCount,
			StreamCount:       row.StreamCount,
		})
		addGatewaySummary(&out.Summary, row)
	}

	return out, nil
}

func addGatewaySummary(summary *GatewayCapabilitySummary, row db.GatewayCapabilityRow) {
	summary.TotalRequests += row.RequestCount
	summary.UsageMeteredReqs += row.UsageMeteredCount
	summary.RequestOnlyReqs += row.RequestOnlyCount
	summary.PassthroughReqs += row.PassthroughCount
	summary.StreamRequests += row.StreamCount
	summary.MissingUsageReqs += row.MissingUsageCount
}
