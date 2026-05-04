package writer

import (
	"sync"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/event"
)

type mockSink struct {
	mu      sync.Mutex
	records [][]event.Event
}

func (m *mockSink) InsertEvents(events []event.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, events)
	return nil
}

func (m *mockSink) getRecords() [][]event.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([][]event.Event, len(m.records))
	copy(result, m.records)
	return result
}

type failingSink struct {
	mu        sync.Mutex
	attempts  int
	failUntil int
	delegate  *mockSink
}

func (f *failingSink) InsertEvents(events []event.Event) error {
	f.mu.Lock()
	f.attempts++
	shouldFail := f.attempts <= f.failUntil
	f.mu.Unlock()
	if shouldFail {
		return errDBFail
	}
	return f.delegate.InsertEvents(events)
}

var errDBFail = &dbError{}

type dbError struct{}

func (e *dbError) Error() string { return "db write error" }

func TestBatchWriter_EnqueueAndFlush(t *testing.T) {
	sink := &mockSink{}
	bw := New(sink, 100, 10, 50*time.Millisecond)
	bw.Start()
	defer bw.Stop()

	ev := StatsEvent{Event: event.Event{Path: "/v1/chat/completions", Method: "POST", Status: 200}}
	if !bw.Enqueue(ev) {
		t.Fatal("Enqueue should succeed")
	}

	time.Sleep(100 * time.Millisecond)
	records := sink.getRecords()
	if len(records) == 0 {
		t.Fatal("expected at least one batch flush")
	}
	totalEvents := 0
	for _, batch := range records {
		totalEvents += len(batch)
	}
	if totalEvents != 1 {
		t.Errorf("total events = %d, want 1", totalEvents)
	}
}

func TestBatchWriter_QueueOverflow(t *testing.T) {
	sink := &mockSink{}
	bw := New(sink, 5, 100, 10*time.Second)
	bw.Start()
	defer bw.Stop()

	for i := 0; i < 10; i++ {
		bw.Enqueue(StatsEvent{Event: event.Event{Path: "/test", Method: "POST", Status: 200}})
	}
	_, dropped, _, _ := bw.Snapshot()
	if dropped == 0 {
		t.Error("expected some events to be dropped on overflow")
	}
}

func TestBatchWriter_BatchSizeFlush(t *testing.T) {
	sink := &mockSink{}
	bw := New(sink, 1000, 3, 10*time.Second)
	bw.Start()
	defer bw.Stop()

	for i := 0; i < 6; i++ {
		bw.Enqueue(StatsEvent{Event: event.Event{Path: "/test", Method: "POST", Status: 200}})
	}

	time.Sleep(200 * time.Millisecond)
	records := sink.getRecords()
	totalEvents := 0
	for _, batch := range records {
		totalEvents += len(batch)
	}
	if totalEvents != 6 {
		t.Errorf("total events = %d, want 6", totalEvents)
	}
	batchFound := false
	for _, batch := range records {
		if len(batch) == 3 {
			batchFound = true
		}
	}
	if !batchFound {
		t.Error("expected at least one batch of size 3")
	}
}

func TestBatchWriter_StopDrainsQueue(t *testing.T) {
	sink := &mockSink{}
	bw := New(sink, 1000, 100, 10*time.Second)
	bw.Start()

	for i := 0; i < 5; i++ {
		bw.Enqueue(StatsEvent{Event: event.Event{Path: "/test", Method: "POST", Status: 200}})
	}

	bw.Stop()

	records := sink.getRecords()
	totalEvents := 0
	for _, batch := range records {
		totalEvents += len(batch)
	}
	if totalEvents != 5 {
		t.Errorf("total events after stop = %d, want 5", totalEvents)
	}
}

func TestBatchWriter_EnqueueAfterStopIsDropped(t *testing.T) {
	sink := &mockSink{}
	bw := New(sink, 100, 10, 10*time.Second)
	bw.Start()
	bw.Stop()

	if bw.Enqueue(StatsEvent{Event: event.Event{Path: "/test", Method: "POST", Status: 200}}) {
		t.Fatal("Enqueue after Stop should fail")
	}
	_, dropped, _, _ := bw.Snapshot()
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
}

func TestBatchWriter_Snapshot(t *testing.T) {
	sink := &mockSink{}
	bw := New(sink, 100, 10, 10*time.Second)

	bw.Enqueue(StatsEvent{Event: event.Event{Path: "/test"}})
	bw.IncrParseErrors()

	qd, dropped, parseErrors, dbErrors := bw.Snapshot()
	if qd != 1 {
		t.Errorf("queue depth = %d, want 1", qd)
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if parseErrors != 1 {
		t.Errorf("parseErrors = %d, want 1", parseErrors)
	}
	if dbErrors != 0 {
		t.Errorf("dbErrors = %d, want 0", dbErrors)
	}
}

func TestBatchWriter_RetryOnDBError(t *testing.T) {
	delegate := &mockSink{}
	sink := &failingSink{failUntil: 1, delegate: delegate}
	bw := New(sink, 100, 100, 50*time.Millisecond)
	bw.Start()
	defer bw.Stop()

	bw.Enqueue(StatsEvent{Event: event.Event{Path: "/test", Method: "POST", Status: 200}})

	time.Sleep(200 * time.Millisecond)
	records := delegate.getRecords()
	totalEvents := 0
	for _, batch := range records {
		totalEvents += len(batch)
	}
	if totalEvents != 1 {
		t.Errorf("total events after retry = %d, want 1", totalEvents)
	}
}
