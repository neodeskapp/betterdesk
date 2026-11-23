package audit

import (
	"log"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// Store keeps the events in the database; db.Database is one.
type Store interface {
	AddServerEvents(events []*db.ServerEvent) error
	ListServerEvents(action string, limit, offset int) ([]*db.ServerEvent, error)
	CountServerEvents(action string) (int64, error)
	PruneServerEvents(cutoff time.Time) (int64, error)
}

// RetentionDays is how long the store keeps an event: once an hour the
// writer deletes older ones, as the console's housekeeping keeps its audit
// log (DECISIONS A93, A124).
const RetentionDays = 90

const (
	// queueSize is how many events wait for the writer before Log waits.
	queueSize = 4096
	// maxBatch is the most events the writer stores in one transaction.
	maxBatch = 256
)

// queued is an event for the writer, or with done a flush: the writer
// closes done when everything queued before it is stored.
type queued struct {
	event *db.ServerEvent
	done  chan struct{}
}

// UseStore makes the logger keep its events in store from now on: Log
// queues them for a writer that stores them in batches and deletes those
// older than RetentionDays at once and every hour, and the readers read
// the store. Close stores what is queued. A second call changes nothing.
func (l *Logger) UseStore(store Store) {
	l.qmu.Lock()
	defer l.qmu.Unlock()
	if l.store != nil || l.closed {
		return
	}
	l.store = store
	l.queue = make(chan queued, queueSize)
	l.writerDone = make(chan struct{})
	go write(store, l.queue, l.writerDone)
}

// enqueue hands the event to the writer and reports whether there is one.
func (l *Logger) enqueue(e Event) bool {
	l.qmu.RLock()
	defer l.qmu.RUnlock()
	if l.queue == nil || l.closed {
		return false
	}
	l.queue <- queued{event: &db.ServerEvent{Time: e.Timestamp, Action: string(e.Action), Actor: e.Actor, Target: e.Target, Details: e.Details}}
	return true
}

// storeOf is the store once everything queued so far is in it, nil
// without one.
func (l *Logger) storeOf() Store {
	l.qmu.RLock()
	store := l.store
	var done chan struct{}
	if l.queue != nil && !l.closed {
		done = make(chan struct{})
		l.queue <- queued{done: done}
	}
	l.qmu.RUnlock()
	if done != nil {
		<-done
	}
	return store
}

// stopWriter stores what is queued and waits for the writer to end.
func (l *Logger) stopWriter() {
	l.qmu.Lock()
	if l.queue != nil && !l.closed {
		close(l.queue)
	}
	l.closed = true
	done := l.writerDone
	l.qmu.Unlock()
	if done != nil {
		<-done
	}
}

// write stores what comes from queue, in batches, until it is closed, and
// prunes the store at start and every hour.
func write(store Store, queue <-chan queued, done chan<- struct{}) {
	defer close(done)
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	pruneStore(store)
	for {
		select {
		case first, ok := <-queue:
			if !ok {
				return
			}
			if !writeBatch(store, first, queue) {
				return
			}
		case <-prune.C:
			pruneStore(store)
		}
	}
}

// writeBatch stores first and what else is queued, up to maxBatch events,
// and ends the flushes among them. It reports false when the queue is
// closed.
func writeBatch(store Store, first queued, queue <-chan queued) bool {
	var batch []*db.ServerEvent
	var flushes []chan struct{}
	take := func(q queued) {
		if q.event != nil {
			batch = append(batch, q.event)
		}
		if q.done != nil {
			flushes = append(flushes, q.done)
		}
	}
	take(first)
	open := true
more:
	for len(batch) < maxBatch {
		select {
		case q, ok := <-queue:
			if !ok {
				open = false
				break more
			}
			take(q)
		default:
			break more
		}
	}
	if err := store.AddServerEvents(batch); err != nil {
		log.Printf("[audit] %d events were not stored: %v", len(batch), err)
	}
	for _, f := range flushes {
		close(f)
	}
	return open
}

func pruneStore(store Store) {
	n, err := store.PruneServerEvents(time.Now().AddDate(0, 0, -RetentionDays))
	if err != nil {
		log.Printf("[audit] WARN: pruning the server events: %v", err)
	} else if n > 0 {
		log.Printf("[audit] Removed %d server events older than %d days", n, RetentionDays)
	}
}

// storedPage is Page on the store.
func storedPage(store Store, action Action, limit, offset int) ([]Event, int64, error) {
	total, err := store.CountServerEvents(string(action))
	if err != nil {
		return nil, 0, err
	}
	if limit == 0 {
		return nil, total, nil
	}
	rows, err := store.ListServerEvents(string(action), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, Event{Timestamp: row.Time, Action: Action(row.Action), Actor: row.Actor, Target: row.Target, Details: row.Details})
	}
	return events, total, nil
}
