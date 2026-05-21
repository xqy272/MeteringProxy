package store

import (
	"time"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/event"
)

// recordSink is the concrete record-level boundary implemented by SQLite.
type recordSink interface {
	InsertBatch(records []db.UsageRecord) error
}

// EventSink is the write-side interface for persisting domain usage events.
// The writer depends on this, not on database row types.
type EventSink interface {
	InsertEvents(events []event.Event) error
}

type eventSink struct {
	records recordSink
}

func NewEventSink(records recordSink) EventSink {
	return eventSink{records: records}
}

func (s eventSink) InsertEvents(events []event.Event) error {
	records := make([]db.UsageRecord, len(events))
	for i, ev := range events {
		records[i] = event.EventToRecord(ev)
	}
	return s.records.InsertBatch(records)
}

// ReportStore is the read-side interface for querying usage reports.
// The WebUI depends on this, not on concrete SQL details.
type ReportStore interface {
	Summary(since time.Time) (*db.SummaryRow, error)
	Models(since time.Time) ([]db.ModelRow, error)
	ModelSourceCounts(since time.Time, model string) (map[string]int64, map[string]int64, error)
	Keys(since time.Time) ([]db.KeyRow, error)
	Timeseries(since time.Time, bucketMin int) ([]db.TimeseriesRow, error)
	ModelTimeseries(since time.Time, bucketMin int) ([]db.ModelTimeseriesRow, error)
	Activity(since time.Time) (*db.ActivityRow, error)
	Requests(limit int, statusMin, statusMax int, model, endpoint, errorClass string, since time.Time) ([]db.RequestRow, error)
	ErrorTimeline(since time.Time) ([]db.ErrorTimelineRow, error)
	ErrorTimelineFromRequests(since time.Time) ([]db.ErrorTimelineRow, error)
	LatestHealth() (*db.HealthRow, error)
	Overview(since time.Time) *db.OverviewRow
	Issues(since time.Time, limit int) ([]db.IssueRow, error)
	OverviewCaptureStats(since time.Time) (failed, skipped int64, err error)
	CaptureOutcomeCounts(since time.Time) (captured, skipped, failed int64, err error)
	AllCredentialHealth() ([]db.CredentialHealthRow, error)
	AllQuotaCurrent() ([]db.QuotaCurrentRow, error)
	RecentQuotaRefreshEvents(since time.Time, limit int) ([]db.QuotaRefreshEventRow, error)
	SideUsageStatusCounts(since time.Time) (map[string]int64, error)
	MultimodalSummary(since time.Time) ([]db.MultimodalSummaryRow, error)
	ImageSummary(since time.Time) (*db.ImageSummaryRow, error)
	ImageModels(since time.Time) ([]db.ImageModelRow, error)
	ImageRequests(limit int, since time.Time) ([]db.RequestRow, error)
}

// HealthWriter is the write-side interface for health metrics.
type HealthWriter interface {
	InsertHealthMetric(ts string, queueDepth int, dropped, parseErrors, dbErrors, sseLineSkips int64) error
}

type SideUsageStore interface {
	InsertSideUsageEvent(db.SideUsageEvent) (int64, error)
	ApplySideUsageEvent(int64, time.Duration) (string, error)
	DeleteStaleSideUsageEvents(time.Time) error
}

type CredentialHealthStore interface {
	UpsertCredentialHealth(*db.CredentialHealthRow) error
	AllCredentialHealth() ([]db.CredentialHealthRow, error)
	DeleteStaleCredentialHealth(time.Time) error
}

type QuotaStore interface {
	UpsertQuotaCurrent(*db.QuotaCurrentRow) error
	AllQuotaCurrent() ([]db.QuotaCurrentRow, error)
	InsertQuotaRefreshEvent(*db.QuotaRefreshEventRow) error
	DeleteStaleQuotaRefreshEvents(time.Time) error
}
