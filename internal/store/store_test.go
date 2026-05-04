package store

import (
	"errors"
	"testing"

	"ai-gateway-metering-proxy/internal/db"
	"ai-gateway-metering-proxy/internal/event"
)

type fakeRecordSink struct {
	inserted []db.UsageRecord
	fail     bool
}

func (f *fakeRecordSink) InsertBatch(records []db.UsageRecord) error {
	if f.fail {
		return errors.New("insert failed")
	}
	f.inserted = append(f.inserted, records...)
	return nil
}

func TestInsertEvents_ConvertsCorrectly(t *testing.T) {
	sink := &fakeRecordSink{}
	es := NewEventSink(sink)

	ev := event.Event{
		ID:             "req-1",
		Path:           "/v1/chat/completions",
		Method:         "POST",
		Status:         200,
		Stream:         true,
		LatencyMs:      100,
		TTFBMs:         30,
		APIKeyHash:     "key-hash",
		ClientIPHash:   "ip-hash",
		ModelReturned:  "gpt-4o",
		InputTokens:    10,
		OutputTokens:   20,
		TotalTokens:    30,
		CaptureOutcome: event.OutcomeCaptured,
	}
	events := []event.Event{ev}

	if err := es.InsertEvents(events); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}
	if len(sink.inserted) != 1 {
		t.Fatalf("inserted = %d, want 1", len(sink.inserted))
	}
	r := sink.inserted[0]
	if r.RequestID != "req-1" || r.Endpoint != "/v1/chat/completions" || r.TotalTokens != 30 {
		t.Errorf("unexpected record: %+v", r)
	}
}

func TestInsertEvents_EmptySlice(t *testing.T) {
	sink := &fakeRecordSink{}
	es := NewEventSink(sink)
	if err := es.InsertEvents(nil); err != nil {
		t.Fatalf("InsertEvents(nil): %v", err)
	}
	if err := es.InsertEvents([]event.Event{}); err != nil {
		t.Fatalf("InsertEvents(empty): %v", err)
	}
	if len(sink.inserted) != 0 {
		t.Errorf("inserted = %d, want 0 for empty input", len(sink.inserted))
	}
}

func TestInsertEvents_PropagatesError(t *testing.T) {
	sink := &fakeRecordSink{fail: true}
	es := NewEventSink(sink)
	err := es.InsertEvents([]event.Event{{}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
