package store

import (
	"context"
	"time"

	"kubescope/internal/model"
)

// Storage persists events. SQLite is the first implementation; the interface
// leaves room for bigger backends (PostgreSQL, ClickHouse) on busy clusters.
type Storage interface {
	// Insert stores events whose IDs are already assigned. Details with
	// KeepBody=false are stored without request/response bodies.
	Insert(ctx context.Context, rows []Row) error
	Get(ctx context.Context, id uint64) (model.Detail, bool, error)
	MaxID(ctx context.Context) (uint64, error)
	// Recent returns the newest n events, oldest first.
	Recent(ctx context.Context, n int) ([]model.Event, error)
	Range(ctx context.Context, q Query) (RangeResult, error)
	Histogram(ctx context.Context, from, to time.Time, buckets int) (Histogram, error)
	Info(ctx context.Context) (Info, error)
	// Prune deletes events older than cutoff, then the oldest events until the
	// database is under maxBytes (0 = no size limit). Returns rows deleted by
	// each rule. It also does routine maintenance (e.g. trimming the SQLite WAL).
	Prune(ctx context.Context, cutoff time.Time, maxBytes int64) (byAge, bySize int64, err error)
	Close() error
}

type Row struct {
	Detail   model.Detail
	KeepBody bool
}

type Query struct {
	From, To  time.Time
	HideNoise []string
	Limit     int
}

type RangeResult struct {
	Events      []model.Event  `json:"events"`
	Total       int            `json:"total"`
	Truncated   bool           `json:"truncated"`
	NoiseCounts map[string]int `json:"noiseCounts"` // per category, hidden ones included
}

type Bucket struct {
	Time   time.Time `json:"time"`
	Total  int       `json:"total"`  // everything, noise included
	Shown  int       `json:"shown"`  // without noise
	Writes int       `json:"writes"` // writes without noise
}

type Histogram struct {
	From     time.Time     `json:"from"`
	To       time.Time     `json:"to"`
	BucketMs int64         `json:"bucketMs"`
	Buckets  []Bucket      `json:"buckets"`
	Markers  []model.Event `json:"markers"`
}

type Info struct {
	Count  int        `json:"count"`
	Oldest *time.Time `json:"oldest,omitempty"`
	Newest *time.Time `json:"newest,omitempty"`
	Bytes  int64      `json:"bytes"`

	// Filled by Store: the limits, and whether the size limit is cutting history short.
	RetentionHours float64 `json:"retentionHours"`
	MaxBytes       int64   `json:"maxBytes"`
	NoisePolicy    string  `json:"noisePolicy"`
	SizeLimited    bool    `json:"sizeLimited"`
}
