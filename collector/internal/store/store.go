// Package store persists events and fans new ones out to live subscribers.
package store

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"kubescope/internal/model"
)

// NoisePolicy decides how much of a noise event (lease, heartbeat, watch...) is stored.
// Noise is always shown live; the policy only affects history.
type NoisePolicy string

const (
	NoiseSmart NoisePolicy = "smart" // drop pure noise, keep the rest without bodies (default)
	NoiseMeta  NoisePolicy = "meta"  // store all noise without bodies
	NoiseFull  NoisePolicy = "full"  // store everything
	NoiseNone  NoisePolicy = "none"  // never store noise
)

// pureNoise is never stored by the smart policy: it repeats every few seconds
// and has no value in history. On an idle cluster it is about 80% of all events.
var pureNoise = map[string]bool{
	model.NoiseLease:     true,
	model.NoiseHealth:    true,
	model.NoiseInternal:  true,
	model.NoiseDiscovery: true,
}

// keep says whether an event with this noise category is stored, and with bodies or not.
func (p NoisePolicy) keep(noise string) (store, body bool) {
	if noise == "" {
		return true, true
	}
	switch p {
	case NoiseFull:
		return true, true
	case NoiseNone:
		return false, false
	case NoiseSmart:
		return !pureNoise[noise], false
	}
	return true, false
}

func (p NoisePolicy) Valid() bool {
	return p == NoiseSmart || p == NoiseMeta || p == NoiseFull || p == NoiseNone
}

type Options struct {
	Noise     NoisePolicy
	Retention time.Duration // 0 = keep forever
	MaxBytes  int64         // 0 = no size limit
}

const (
	flushEvery = 250 * time.Millisecond
	flushSize  = 1000
	queueSize  = 100_000
)

// Store assigns IDs, writes events in batches and notifies subscribers once
// the events are committed, so anything a browser sees can be fetched by ID.
type Store struct {
	db    Storage
	opts  Options
	queue chan model.Detail

	mu     sync.Mutex // held while committing + broadcasting, and while subscribing
	nextID uint64
	subs   map[chan Message]struct{}

	dropped     atomic.Uint64
	sizeLimited atomic.Bool // the size limit has deleted events younger than the retention
	done        chan struct{}
}

// Message is what live subscribers receive.
type Message struct {
	Type   string        `json:"type"` // "backlog" | "events"
	Events []model.Event `json:"events"`
}

func New(db Storage, opts Options) (*Store, error) {
	maxID, err := db.MaxID(context.Background())
	if err != nil {
		return nil, err
	}
	s := &Store{
		db:     db,
		opts:   opts,
		queue:  make(chan model.Detail, queueSize),
		nextID: maxID + 1,
		subs:   map[chan Message]struct{}{},
		done:   make(chan struct{}),
	}
	go s.writer()
	go s.pruner()
	return s, nil
}

// Add queues events for storage. It never blocks: the audit webhook must
// answer the API server quickly. If the queue is full, events are dropped.
func (s *Store) Add(details []model.Detail) {
	for _, d := range details {
		select {
		case s.queue <- d:
		default:
			if n := s.dropped.Add(1); n%1000 == 1 {
				log.Printf("store: queue full, dropped %d events so far", n)
			}
		}
	}
}

func (s *Store) Get(ctx context.Context, id uint64) (model.Detail, bool, error) {
	return s.db.Get(ctx, id)
}

func (s *Store) Range(ctx context.Context, q Query) (RangeResult, error) { return s.db.Range(ctx, q) }

func (s *Store) Histogram(ctx context.Context, from, to time.Time, buckets int) (Histogram, error) {
	return s.db.Histogram(ctx, from, to, buckets)
}

// Info describes what history is available and the limits that shape it.
func (s *Store) Info(ctx context.Context) (Info, error) {
	info, err := s.db.Info(ctx)
	info.RetentionHours = s.opts.Retention.Hours()
	info.MaxBytes = s.opts.MaxBytes
	info.NoisePolicy = string(s.opts.Noise)
	// Report the size limit while it shortens history: it pruned during this run, or
	// (after a restart) the file sits near the limit while history is shorter than the retention.
	nearLimit := s.opts.MaxBytes > 0 && info.Bytes >= s.opts.MaxBytes*85/100
	info.SizeLimited = (s.sizeLimited.Load() || nearLimit) && s.opts.Retention > 0 &&
		info.Oldest != nil && time.Since(*info.Oldest) < s.opts.Retention
	return info, err
}

// Subscribe returns the newest `backlog` events and a channel of new ones.
// Holding mu means no batch is committed in between, so nothing is missed or duplicated.
func (s *Store) Subscribe(ctx context.Context, backlog int) ([]model.Event, chan Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events, err := s.db.Recent(ctx, backlog)
	if err != nil {
		return nil, nil, err
	}
	ch := make(chan Message, 256)
	s.subs[ch] = struct{}{}
	return events, ch, nil
}

func (s *Store) Unsubscribe(ch chan Message) {
	s.mu.Lock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
	s.mu.Unlock()
}

// Close flushes queued events and stops the background work.
func (s *Store) Close() error {
	close(s.queue)
	<-s.done
	return s.db.Close()
}

func (s *Store) writer() {
	defer close(s.done)
	batch := make([]model.Detail, 0, flushSize)
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	for {
		select {
		case d, ok := <-s.queue:
			if !ok {
				s.flush(batch)
				return
			}
			batch = append(batch, d)
			if len(batch) >= flushSize {
				s.flush(batch)
				batch = batch[:0]
			}
		case <-tick.C:
			s.flush(batch)
			batch = batch[:0]
		}
	}
}

func (s *Store) flush(batch []model.Detail) {
	if len(batch) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rows := make([]Row, 0, len(batch))
	out := make([]model.Event, len(batch))
	for i := range batch {
		batch[i].ID = s.nextID
		s.nextID++
		out[i] = batch[i].Event
		if store, body := s.opts.Noise.keep(batch[i].Noise); store {
			rows = append(rows, Row{Detail: batch[i], KeepBody: body})
		}
	}
	// Retry instead of losing events: the database can be busy for a moment
	// (pruning, a backup, another tool reading or writing the file).
	var err error
	for attempt, wait := 1, 100*time.Millisecond; attempt <= 5; attempt, wait = attempt+1, wait*2 {
		if err = s.db.Insert(context.Background(), rows); err == nil {
			break
		}
		if attempt < 5 {
			time.Sleep(wait)
		}
	}
	if err != nil {
		log.Printf("store: could not save %d events after 5 attempts, they are shown live but missing from history: %v", len(rows), err)
	}
	s.broadcast(Message{Type: "events", Events: out})
}

// broadcast must be called with mu held. A subscriber that can't keep up is
// dropped instead of slowing everyone down; the browser reconnects.
func (s *Store) broadcast(m Message) {
	for ch := range s.subs {
		select {
		case ch <- m:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}

func (s *Store) pruner() {
	if s.opts.Retention == 0 && s.opts.MaxBytes == 0 {
		return
	}
	for {
		cutoff := time.Time{}
		if s.opts.Retention > 0 {
			cutoff = time.Now().Add(-s.opts.Retention)
		}
		byAge, bySize, err := s.db.Prune(context.Background(), cutoff, s.opts.MaxBytes)
		switch {
		case err != nil:
			log.Printf("store: prune: %v", err)
		case bySize > 0:
			s.sizeLimited.Store(true)
			kept := "nothing"
			if info, err := s.db.Info(context.Background()); err == nil && info.Oldest != nil {
				kept = time.Since(*info.Oldest).Round(time.Minute).String()
			}
			hint := "Raise -max-db-mb"
			if s.opts.Noise != NoiseNone {
				hint += ", or store less noise with -store-noise=none"
			}
			hint += ", or use the production audit policy (AUDIT_POLICY=production cluster/up.sh)."
			log.Printf("store: WARNING size limit of %d MB reached, deleted %d oldest events. History now covers %s instead of %s. %s",
				s.opts.MaxBytes>>20, bySize, kept, s.opts.Retention, hint)
		case byAge > 0:
			log.Printf("store: deleted %d events older than %s", byAge, s.opts.Retention)
		}
		select {
		case <-s.done:
			return
		case <-time.After(5 * time.Minute):
		}
	}
}
