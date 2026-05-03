package writer

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-gateway-metering-proxy/internal/event"
)

type fakeInserter struct {
	mu        sync.Mutex
	failures  int
	calls     int
	inserted  []event.Event
	callReady chan struct{}
}

func (f *fakeInserter) InsertEvents(events []event.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.callReady != nil {
		select {
		case <-f.callReady:
		default:
			close(f.callReady)
		}
	}
	if f.failures > 0 {
		f.failures--
		return errors.New("temporary insert failure")
	}
	f.inserted = append(f.inserted, events...)
	return nil
}

func (f *fakeInserter) snapshot() (calls int, inserted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, len(f.inserted)
}

func evWithTime(ts string) StatsEvent {
	return StatsEvent{Event: event.Event{
		Timestamp: time.Now().UTC(),
		Path:      "/v1/responses",
		Method:    "POST",
		Status:    200,
	}}
}

func TestEnqueueDropsWhenQueueFull(t *testing.T) {
	bw := &BatchWriter{
		queue:       make(chan StatsEvent, 1),
		db:          &fakeInserter{},
		batchSize:   10,
		flushTicker: time.NewTicker(time.Hour),
		stopCh:      make(chan struct{}),
	}
	defer bw.flushTicker.Stop()

	if !bw.Enqueue(StatsEvent{}) {
		t.Fatal("first enqueue should fit")
	}
	if bw.Enqueue(StatsEvent{}) {
		t.Fatal("second enqueue should be dropped")
	}
	if got := atomic.LoadInt64(&bw.DroppedEvents); got != 1 {
		t.Fatalf("DroppedEvents = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&bw.QueueDepth); got != 1 {
		t.Fatalf("QueueDepth = %d, want 1", got)
	}
}

func TestStopDrainsQueue(t *testing.T) {
	fake := &fakeInserter{}
	bw := &BatchWriter{
		queue:       make(chan StatsEvent, 10),
		db:          fake,
		batchSize:   50,
		flushTicker: time.NewTicker(time.Hour),
		stopCh:      make(chan struct{}),
	}
	bw.Start()

	for i := 0; i < 3; i++ {
		if !bw.Enqueue(evWithTime("")) {
			t.Fatalf("enqueue %d dropped", i)
		}
	}
	bw.Stop()

	_, inserted := fake.snapshot()
	if inserted != 3 {
		t.Fatalf("inserted = %d, want 3", inserted)
	}
	if qd, _, _, _ := bw.Snapshot(); qd != 0 {
		t.Fatalf("QueueDepth = %d, want 0", qd)
	}
}

func TestFlushRetriesOnceOnInsertFailure(t *testing.T) {
	fake := &fakeInserter{failures: 1, callReady: make(chan struct{})}
	bw := &BatchWriter{
		queue:       make(chan StatsEvent, 10),
		db:          fake,
		batchSize:   1,
		flushTicker: time.NewTicker(time.Hour),
		stopCh:      make(chan struct{}),
	}
	bw.Start()
	defer bw.Stop()

	if !bw.Enqueue(evWithTime("")) {
		t.Fatal("enqueue dropped")
	}

	select {
	case <-fake.callReady:
	case <-time.After(2 * time.Second):
		t.Fatal("insert was not attempted")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		calls, inserted := fake.snapshot()
		if calls >= 2 && inserted == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retry did not succeed; calls=%d inserted=%d", calls, inserted)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, _, _, dbErrors := bw.Snapshot(); dbErrors != 0 {
		t.Fatalf("DBWriteErrors = %d, want 0 after retry success", dbErrors)
	}
}

func TestFlushCountsDBErrorAfterRetryFailure(t *testing.T) {
	fake := &fakeInserter{failures: 2, callReady: make(chan struct{})}
	bw := &BatchWriter{
		queue:       make(chan StatsEvent, 10),
		db:          fake,
		batchSize:   1,
		flushTicker: time.NewTicker(time.Hour),
		stopCh:      make(chan struct{}),
	}
	bw.Start()
	defer bw.Stop()

	if !bw.Enqueue(evWithTime("")) {
		t.Fatal("enqueue dropped")
	}

	select {
	case <-fake.callReady:
	case <-time.After(2 * time.Second):
		t.Fatal("insert was not attempted")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		calls, inserted := fake.snapshot()
		_, _, _, dbErrors := bw.Snapshot()
		if calls >= 2 && inserted == 0 && dbErrors == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retry failure was not counted; calls=%d inserted=%d dbErrors=%d", calls, inserted, dbErrors)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
