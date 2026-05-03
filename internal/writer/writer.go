package writer

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"ai-gateway-metering-proxy/internal/event"
	"ai-gateway-metering-proxy/internal/metrics"
	"ai-gateway-metering-proxy/internal/store"
)

// StatsEvent wraps a domain event for the writer queue.
type StatsEvent struct {
	Event event.Event
}

type BatchWriter struct {
	queue       chan StatsEvent
	db          store.EventSink
	batchSize   int
	flushTicker *time.Ticker
	stopCh      chan struct{}
	wg          sync.WaitGroup

	QueueDepth    int64
	ParseErrors   int64
	DBWriteErrors int64
	DroppedEvents int64
}

func New(database store.EventSink, queueCapacity, batchSize int, flushInterval time.Duration) *BatchWriter {
	return &BatchWriter{
		queue:       make(chan StatsEvent, queueCapacity),
		db:          database,
		batchSize:   batchSize,
		flushTicker: time.NewTicker(flushInterval),
		stopCh:      make(chan struct{}),
	}
}

func (bw *BatchWriter) Start() {
	bw.wg.Add(1)
	go bw.loop()
}

func (bw *BatchWriter) Stop() {
	close(bw.stopCh)
	bw.flushTicker.Stop()
	bw.wg.Wait()
}

func (bw *BatchWriter) Enqueue(ev StatsEvent) bool {
	select {
	case bw.queue <- ev:
		atomic.AddInt64(&bw.QueueDepth, 1)
		metrics.SetQueueDepth(atomic.LoadInt64(&bw.QueueDepth))
		return true
	default:
		atomic.AddInt64(&bw.DroppedEvents, 1)
		metrics.AddDroppedEvents(1)
		return false
	}
}

func (bw *BatchWriter) loop() {
	defer bw.wg.Done()
	batch := make([]event.Event, 0, bw.batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := bw.db.InsertEvents(batch); err != nil {
			time.Sleep(100 * time.Millisecond)
			if err2 := bw.db.InsertEvents(batch); err2 != nil {
				atomic.AddInt64(&bw.DBWriteErrors, 1)
				metrics.AddDBWriteErrors(1)
				log.Printf("batch insert error (after retry): %v", err2)
			}
		}
		atomic.AddInt64(&bw.QueueDepth, -int64(len(batch)))
		metrics.SetQueueDepth(atomic.LoadInt64(&bw.QueueDepth))
		batch = batch[:0]
	}

	for {
		select {
		case <-bw.stopCh:
			for {
				select {
				case ev := <-bw.queue:
					batch = append(batch, ev.Event)
					if len(batch) >= bw.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case ev := <-bw.queue:
			batch = append(batch, ev.Event)
			if len(batch) >= bw.batchSize {
				flush()
			}
		case <-bw.flushTicker.C:
			flush()
		}
	}
}

func (bw *BatchWriter) Snapshot() (queueDepth, dropped, parseErrors, dbErrors int64) {
	return atomic.LoadInt64(&bw.QueueDepth),
		atomic.LoadInt64(&bw.DroppedEvents),
		atomic.LoadInt64(&bw.ParseErrors),
		atomic.LoadInt64(&bw.DBWriteErrors)
}

func (bw *BatchWriter) IncrParseErrors() {
	atomic.AddInt64(&bw.ParseErrors, 1)
	metrics.AddParseErrors(1)
}
