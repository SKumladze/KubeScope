package store

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"kubescope/internal/model"
)

// Writes must never fail because the UI is reading at the same time.
// Before the fix this failed with "database is locked (517)" (SQLITE_BUSY_SNAPSHOT):
// events showed up live but were never saved.
func TestInsertsSurviveConcurrentReads(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "busy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var reads atomic.Int64
	// What the browser does: histogram, range, info and detail lookups, in parallel.
	for r := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				now := time.Now()
				switch r % 4 {
				case 0:
					db.Histogram(ctx, now.Add(-time.Hour), now, 120)
				case 1:
					db.Range(ctx, Query{From: now.Add(-time.Hour), To: now, Limit: 500})
				case 2:
					db.Info(ctx)
				case 3:
					db.Recent(ctx, 200)
				}
				reads.Add(1)
			}
		}()
	}
	// Browsers cancel requests (the activity bar aborts its fetch whenever the window moves).
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				cctx, cancel := context.WithTimeout(ctx, time.Duration(50+time.Now().UnixNano()%400)*time.Microsecond)
				now := time.Now()
				db.Histogram(cctx, now.Add(-time.Hour), now, 120)
				db.Range(cctx, Query{From: now.Add(-time.Hour), To: now, Limit: 2000})
				cancel()
			}
		}()
	}
	// Someone else writes to the file now and then (another tool, a cleanup script).
	other, err := OpenSQLite(db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(30 * time.Millisecond):
				other.w.Exec(`DELETE FROM events WHERE id < 0`)
				other.w.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
			}
		}
	}()
	// Pruning runs alongside, like the background pruner.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
				db.Prune(ctx, time.Now().Add(-time.Hour), 0)
			}
		}
	}()

	failures := 0
	id := uint64(1)
	for range 400 {
		var rows []Row
		for range 5 {
			d := model.Detail{Event: model.Event{ID: id, Time: time.Now(), Source: "audit", From: "kubectl", Verb: "get"}}
			rows = append(rows, Row{Detail: d, KeepBody: true})
			id++
		}
		if err := db.Insert(ctx, rows); err != nil {
			failures++
			if failures == 1 {
				t.Logf("first failure: %v", err)
			}
		}
	}
	close(stop)
	wg.Wait()

	if failures > 0 {
		t.Fatalf("%d of 400 inserts failed while %d reads ran concurrently", failures, reads.Load())
	}
	var stored, viaWriter int
	db.w.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&viaWriter)
	t.Logf("count via writer: %d", viaWriter)
	// Every reader connection in the pool must see the latest data too.
	for i := range 8 {
		var n int
		db.r.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n)
		t.Logf("count via reader #%d: %d", i, n)
	}
	db.r.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&stored)
	if stored != 2000 {
		t.Fatalf("stored %d of 2000 events", stored)
	}
}
