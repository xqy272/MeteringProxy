package report

import (
	"context"
	"fmt"
)

func (s *Service) ImageRequests(ctx context.Context, filter ImageRequestsFilter) ([]RequestReport, error) {
	if s == nil {
		return nil, fmt.Errorf("report service is not configured")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.imageRequests.ImageRequestsReport(ctx, limit, filter.Since)
	if err != nil {
		return nil, err
	}
	out := make([]RequestReport, len(rows))
	for i, row := range rows {
		out[i] = requestReportFromRow(row)
	}
	return out, nil
}
