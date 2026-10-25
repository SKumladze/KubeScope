package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kubescope/internal/model"
)

func newStore(t *testing.T, opts Options) (*Store, *SQLite) {
	t.Helper()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, db
}

func ev(at time.Time, noise string, body string) model.Detail {
	d := model.Detail{Event: model.Event{Time: at, Source: "audit", From: "kubectl", Verb: "create", Noise: noise, Summary: "x"}}
	if body != "" {
		d.RequestObject = json.RawMessage(body)
	}
	return d
}

// addAndWait subscribes, adds events and returns them once they are committed.
func addAndWait(t *testing.T, s *Store, details ...model.Detail) []model.Event {
	t.Helper()
	_, ch, err := s.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Unsubscribe(ch)
	s.Add(details)
	var got []model.Event
	timeout := time.After(3 * time.Second)
	for len(got) < len(details) {
		select {
		case m := <-ch:
			got = append(got, m.Events...)
		case <-timeout:
			t.Fatalf("got %d of %d events", len(got), len(details))
		}
	}
	return got
}

func TestBodiesFollowNoisePolicy(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _ := newStore(t, Options{Noise: NoiseMeta})
	got := addAndWait(t, s, ev(now, "", `{"kind":"Pod"}`), ev(now, "lease", `{"kind":"Lease"}`))

	d, ok, err := s.Get(ctx, got[0].ID)
	if err != nil || !ok || string(d.RequestObject) != `{"kind":"Pod"}` {
		t.Fatalf("normal event: ok=%v err=%v body=%s", ok, err, d.RequestObject)
	}
	d, ok, _ = s.Get(ctx, got[1].ID)
	if !ok || d.RequestObject != nil {
		t.Fatalf("noise event should be stored without body, ok=%v body=%s", ok, d.RequestObject)
	}

	s2, _ := newStore(t, Options{Noise: NoiseNone})
	got = addAndWait(t, s2, ev(now, "lease", ""))
	if _, ok, _ := s2.Get(ctx, got[0].ID); ok {
		t.Fatal("noise must not be stored with -store-noise=none")
	}

	// smart: pure noise dropped, other noise kept without bodies, real events in full.
	s3, _ := newStore(t, Options{Noise: NoiseSmart})
	got = addAndWait(t, s3,
		ev(now, "lease", `{}`), ev(now, "health", `{}`), ev(now, "internal", `{}`), ev(now, "discovery", `{}`),
		ev(now, "watch", `{"w":1}`), ev(now, "", `{"kind":"Pod"}`))
	for _, e := range got[:4] {
		if _, ok, _ := s3.Get(ctx, e.ID); ok {
			t.Errorf("smart: %s should not be stored", e.Noise)
		}
	}
	if d, ok, _ := s3.Get(ctx, got[4].ID); !ok || d.RequestObject != nil {
		t.Errorf("smart: watch should be stored without body, ok=%v", ok)
	}
	if d, ok, _ := s3.Get(ctx, got[5].ID); !ok || d.RequestObject == nil {
		t.Errorf("smart: real event should keep its body, ok=%v", ok)
	}
}

func TestRangeHideAndLimit(t *testing.T) {
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)
	s, _ := newStore(t, Options{Noise: NoiseMeta})
	addAndWait(t, s,
		ev(base.Add(-time.Minute), "", ""), // before the range
		ev(base, "", ""),
		ev(base.Add(time.Second), "watch", ""),
		ev(base.Add(2*time.Second), "", ""),
		ev(base.Add(time.Minute), "", ""), // after the range
	)
	q := Query{From: base, To: base.Add(10 * time.Second), Limit: 100}
	if res, _ := s.Range(ctx, q); res.Total != 3 || len(res.Events) != 3 {
		t.Fatalf("all in range: total=%d len=%d", res.Total, len(res.Events))
	}
	q.HideNoise = []string{"watch"}
	if res, _ := s.Range(ctx, q); res.Total != 2 || res.NoiseCounts["watch"] != 1 {
		t.Fatalf("hide watch: total=%d noise=%v", res.Total, res.NoiseCounts)
	}
	q.Limit = 1
	res, _ := s.Range(ctx, q)
	if len(res.Events) != 1 || !res.Truncated || !res.Events[0].Time.Equal(base) {
		t.Fatalf("limit: %+v", res)
	}
}

func TestHistogramAndMarkers(t *testing.T) {
	ctx := context.Background()
	base := time.Now().Truncate(time.Minute)
	s, _ := newStore(t, Options{Noise: NoiseMeta})
	marker := ev(base.Add(30*time.Second), "", "")
	marker.Source = model.SourceMarker
	addAndWait(t, s,
		ev(base.Add(1*time.Second), "", ""),
		ev(base.Add(2*time.Second), "lease", ""),
		marker,
		ev(base.Add(70*time.Second), "", ""),
	)
	h, err := s.Histogram(ctx, base, base.Add(2*time.Minute), 2)
	if err != nil {
		t.Fatal(err)
	}
	if h.Buckets[0].Total != 3 || h.Buckets[0].Shown != 2 || h.Buckets[1].Total != 1 {
		t.Fatalf("buckets: %+v", h.Buckets)
	}
	if len(h.Markers) != 1 {
		t.Fatalf("markers: %d", len(h.Markers))
	}
}

func TestPruneByAgeAndSize(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, db := newStore(t, Options{Noise: NoiseFull})
	big := `{"data":"` + strings.Repeat("x", 4000) + `"}`
	var details []model.Detail
	for i := range 2000 {
		// unique-ish bodies so gzip can't shrink them to nothing
		details = append(details, ev(now.Add(time.Duration(i)*time.Millisecond), "", strings.Replace(big, "xxxx", time.Duration(i).String(), 50)))
	}
	details = append(details, ev(now.Add(-48*time.Hour), "", ""))
	addAndWait(t, s, details...)

	byAge, bySize, err := db.Prune(ctx, now.Add(-24*time.Hour), 0)
	if err != nil || byAge != 1 || bySize != 0 {
		t.Fatalf("age prune: byAge=%d bySize=%d err=%v", byAge, bySize, err)
	}
	before, _ := db.Info(ctx)
	limit := before.Bytes / 2
	if _, bySize, err := db.Prune(ctx, time.Time{}, limit); err != nil || bySize == 0 {
		t.Fatalf("size prune: bySize=%d err=%v", bySize, err)
	}
	after, _ := db.Info(ctx)
	var freePages int
	db.r.QueryRow(`PRAGMA freelist_count`).Scan(&freePages)
	if freePages > 10 {
		t.Errorf("deleted space was not given back to the file system: %d free pages", freePages)
	}
	if after.Bytes > limit || after.Count == 0 || after.Count >= before.Count {
		t.Fatalf("size prune: before=%d bytes/%d rows, after=%d bytes/%d rows, limit=%d",
			before.Bytes, before.Count, after.Bytes, after.Count, limit)
	}
	// The newest events are the ones that survive.
	res, _ := s.Range(ctx, Query{From: now.Add(1999 * time.Millisecond), To: now.Add(time.Hour), Limit: 10})
	if res.Total != 1 {
		t.Fatalf("newest event was pruned")
	}
}

func TestIDsContinueAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	db, _ := OpenSQLite(path)
	s, _ := New(db, Options{Noise: NoiseMeta})
	first := addAndWait(t, s, ev(time.Now(), "", ""))
	s.Close()

	db, _ = OpenSQLite(path)
	s, _ = New(db, Options{Noise: NoiseMeta})
	defer s.Close()
	second := addAndWait(t, s, ev(time.Now(), "", ""))
	if second[0].ID != first[0].ID+1 {
		t.Fatalf("ids: first=%d second=%d", first[0].ID, second[0].ID)
	}
}

func TestHistogramKeepsLastPartialBucket(t *testing.T) {
	base := time.Now().Truncate(time.Second)
	s, _ := newStore(t, Options{Noise: NoiseMeta})
	// 10.000007 s split into 120 buckets does not divide evenly; the very last microsecond must still count.
	end := base.Add(10*time.Second + 7*time.Microsecond)
	addAndWait(t, s, ev(base, "", ""), ev(end.Add(-time.Microsecond), "", ""))
	h, err := s.Histogram(context.Background(), base, end, 120)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, b := range h.Buckets {
		total += b.Total
	}
	if total != 2 || h.Buckets[119].Total != 1 {
		t.Fatalf("total=%d last=%d, want 2 and 1", total, h.Buckets[119].Total)
	}
}
