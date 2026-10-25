package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kubescope/internal/model"
)

// A read cancelled midway (the browser aborting a request) must not leave a reader
// connection stuck on an old snapshot. Before the fix, Range did this about 1 in 75 times.
func TestCancelledReadsDoNotLeaveStaleSnapshots(t *testing.T) {
	ctx := context.Background()
	reads := map[string]func(c context.Context, db *SQLite){
		"Histogram": func(c context.Context, db *SQLite) { db.Histogram(c, time.Now().Add(-time.Hour), time.Now(), 120) },
		"Range":     func(c context.Context, db *SQLite) { db.Range(c, Query{From: time.Now().Add(-time.Hour), To: time.Now(), Limit: 5000}) },
		"Recent":    func(c context.Context, db *SQLite) { db.Recent(c, 5000) },
		"Get":       func(c context.Context, db *SQLite) { db.Get(c, 7) },
	}
	for name, read := range reads {
		db, _ := OpenSQLite(filepath.Join(t.TempDir(), name+".db"))
		db.r.SetMaxOpenConns(1) // one reader connection, so a stuck one is always reused
		id := uint64(1)
		insert := func() {
			var rows []Row
			for range 40 {
				rows = append(rows, Row{Detail: model.Detail{Event: model.Event{ID: id, Time: time.Now(), Source: "audit", From: "kubectl"}}, KeepBody: true})
				id++
			}
			db.Insert(ctx, rows)
		}
		insert()
		stale := 0
		for i := range 300 {
			c, cancel := context.WithTimeout(ctx, time.Duration(20+i%300)*time.Microsecond)
			read(c, db)
			cancel()
			insert()
			var n int
			db.r.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n)
			if n != int(id-1) {
				stale++
			}
		}
		if stale > 0 {
			t.Errorf("%s: %d of 300 reads after a cancelled %s saw an old snapshot", name, stale, name)
		}
		db.Close()
	}
}
