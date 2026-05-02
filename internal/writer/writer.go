package writer

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"ai-gateway-metering-proxy/internal/db"
)

type StatsEvent struct {
	Record db.UsageRecord
}

type batchInserter interface {
	InsertBatch(records []db.UsageRecord) error
}

type BatchWriter struct {
	queue       chan StatsEvent
	db          batchInserter
	batchSize   int
	flushTicker *time.Ticker
	stopCh      chan struct{}
	wg          sync.WaitGroup

	// Metrics
	QueueDepth    int64
	ParseErrors   int64
	DBWriteErrors int64
	DroppedEvents int64
}

func New(database *db.DB, queueCapacity, batchSize int, flushInterval time.Duration) *BatchWriter {
	bw := &BatchWriter{
		queue:       make(chan StatsEvent, queueCapacity),
		db:          database,
		batchSize:   batchSize,
		flushTicker: time.NewTicker(flushInterval),
		stopCh:      make(chan struct{}),
	}
	return bw
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

func (bw *BatchWriter) Enqueue(event StatsEvent) bool {
	select {
	case bw.queue <- event:
		atomic.AddInt64(&bw.QueueDepth, 1)
		return true
	default:
		atomic.AddInt64(&bw.DroppedEvents, 1)
		return false
	}
}

func (bw *BatchWriter) loop() {
	defer bw.wg.Done()
	batch := make([]db.UsageRecord, 0, bw.batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := bw.db.InsertBatch(batch); err != nil {
			time.Sleep(100 * time.Millisecond)
			if err2 := bw.db.InsertBatch(batch); err2 != nil {
				atomic.AddInt64(&bw.DBWriteErrors, 1)
				log.Printf("batch insert error (after retry): %v", err2)
			}
		}
		atomic.AddInt64(&bw.QueueDepth, -int64(len(batch)))
		batch = batch[:0]
	}

	for {
		select {
		case <-bw.stopCh:
			// Drain remaining events
			for {
				select {
				case event := <-bw.queue:
					batch = append(batch, event.Record)
					if len(batch) >= bw.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case event := <-bw.queue:
			batch = append(batch, event.Record)
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
}
