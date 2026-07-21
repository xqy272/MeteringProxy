package report

import (
	"context"
	"fmt"

	"ai-gateway-metering-proxy/internal/db"
)

func (s *Service) MultimodalSummary(ctx context.Context, filter MultimodalFilter) ([]MultimodalSummaryReport, error) {
	if s == nil {
		return nil, fmt.Errorf("report service is not configured")
	}
	rows, err := s.multimodal.MultimodalSummaryReport(ctx, filter.Since)
	if err != nil {
		return nil, err
	}
	out := make([]MultimodalSummaryReport, 0, len(rows))
	for _, row := range rows {
		out = append(out, multimodalSummaryFromRow(row))
	}
	return out, nil
}

func multimodalSummaryFromRow(row db.MultimodalSummaryRow) MultimodalSummaryReport {
	return MultimodalSummaryReport{
		Modality:      row.Modality,
		Channel:       row.Channel,
		Metric:        row.Metric,
		Direction:     row.Direction,
		Unit:          row.Unit,
		Amount:        row.Amount,
		RequestCount:  row.RequestCount,
		UnpricedCount: row.UnpricedCount,
	}
}
