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
