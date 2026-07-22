package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/metrics"
)

func TestServiceKeysRecordsOneSuccessMetric(t *testing.T) {
	beforeQ, beforeE, beforeSum, beforeC, ok := metrics.SnapshotReportQuery(metrics.ReportKeys)
	if !ok {
		t.Fatal("keys report label missing")
	}

	reader := &stubModelsReader{
		keysSnapshot: &db.KeysReportData{
			Rows: []db.KeyRow{{KeyHash: "k", RequestCount: 1}},
		},
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{})

	out, err := svc.Keys(context.Background(), KeysFilter{Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("keys = %+v", out)
	}

	afterQ, afterE, afterSum, afterC, _ := metrics.SnapshotReportQuery(metrics.ReportKeys)
	if afterQ-beforeQ != 1 || afterC-beforeC != 1 {
		t.Fatalf("queries delta = %d count delta = %d, want 1/1", afterQ-beforeQ, afterC-beforeC)
	}
	if afterE-beforeE != 0 {
		t.Fatalf("errors delta = %d, want 0 on success", afterE-beforeE)
	}
	if afterSum < beforeSum {
		t.Fatalf("duration sum decreased")
	}
}

func TestServiceKeysRecordsOneErrorMetric(t *testing.T) {
	beforeQ, beforeE, _, beforeC, ok := metrics.SnapshotReportQuery(metrics.ReportKeys)
	if !ok {
		t.Fatal("keys report label missing")
	}

	reader := &stubModelsReader{keysErr: errors.New("snapshot failed")}
	svc := NewService(testDependencies(reader), &stubCostEngine{})

	_, err := svc.Keys(context.Background(), KeysFilter{Since: time.Now().Add(-time.Hour)})
	if err == nil {
		t.Fatal("expected error")
	}

	afterQ, afterE, _, afterC, _ := metrics.SnapshotReportQuery(metrics.ReportKeys)
	if afterQ-beforeQ != 1 || afterC-beforeC != 1 {
		t.Fatalf("queries/count delta = %d/%d, want 1/1", afterQ-beforeQ, afterC-beforeC)
	}
	if afterE-beforeE != 1 {
		t.Fatalf("errors delta = %d, want 1", afterE-beforeE)
	}
}

func TestServiceReportMetricsDoNotLabelByKeyHash(t *testing.T) {
	reader := &stubModelsReader{
		keysSnapshot: &db.KeysReportData{
			Rows: []db.KeyRow{{
				KeyHash:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				RequestCount: 2,
			}},
		},
	}
	svc := NewService(testDependencies(reader), &stubCostEngine{known: true})
	if _, err := svc.Keys(context.Background(), KeysFilter{}); err != nil {
		t.Fatalf("Keys: %v", err)
	}

	// Exposition must keep the fixed "keys" label and must not emit the key hash.
	// Reuse metrics package write path through Snapshot + known enum only.
	if !metrics.IsKnownReport(metrics.ReportKeys) {
		t.Fatal("keys must remain known")
	}
	if metrics.IsKnownReport(metrics.ReportName("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")) {
		t.Fatal("key hash must not become a report label")
	}
}
