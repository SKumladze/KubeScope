package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go driver, no cgo

	"kubescope/internal/model"
)

const schema = `
CREATE TABLE IF NOT EXISTS events (
	id        INTEGER PRIMARY KEY,
	ts        INTEGER NOT NULL,          -- unix microseconds
	source    TEXT NOT NULL,
	lane      TEXT NOT NULL,
	noise     TEXT NOT NULL,
	write     INTEGER NOT NULL,
	uid       TEXT NOT NULL,
	event     BLOB NOT NULL,             -- model.Event as JSON
	body      BLOB                       -- gzip JSON of bodies, NULL when not kept
);
CREATE INDEX IF NOT EXISTS events_ts ON events(ts);
CREATE INDEX IF NOT EXISTS events_uid ON events(uid) WHERE uid != '';
CREATE INDEX IF NOT EXISTS events_markers ON events(ts) WHERE source = 'marker';
`

// SQLite keeps writes and reads apart, the usual way to run SQLite under load:
//   - w: one connection, the only writer. Its transactions start with BEGIN IMMEDIATE,
//     so they take the write lock up front and wait for it (busy_timeout) instead of
//     starting as a read and failing on upgrade with SQLITE_BUSY_SNAPSHOT (517).
//   - r: a pool of read-only connections for the UI, which can never block or break a write.
type SQLite struct {
	w, r *sql.DB
	path string
}

// OpenSQLite opens (or creates) the database file. WAL lets the UI read
// while the writer inserts; incremental auto-vacuum gives space back after pruning.
func OpenSQLite(path string) (*SQLite, error) {
	w, err := open(path, "immediate", "auto_vacuum(INCREMENTAL)", "journal_mode(WAL)", "synchronous(NORMAL)", "busy_timeout(10000)",
		// After a checkpoint, cut the -wal file back to 8 MB instead of keeping the size of its largest burst.
		"journal_size_limit(8388608)")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(schema); err != nil {
		w.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	r, err := open(path, "deferred", "query_only(1)", "busy_timeout(5000)")
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	// Safety net: recycle reader connections, so one stuck on an old snapshot can't live long.
	r.SetConnMaxLifetime(time.Minute)
	return &SQLite{w: w, r: r, path: path}, nil
}

// readContext detaches a read from cancellation. When a query is cancelled midway
// (the browser aborts a request it no longer needs), the SQLite driver can leave the
// statement open, and that connection keeps showing an old snapshot of the database.
// Before reads and writes were split this also broke inserts with SQLITE_BUSY_SNAPSHOT.
// Reads take milliseconds, so letting them finish costs nothing.
func readContext(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

func open(path, txlock string, pragmas ...string) (*sql.DB, error) {
	q := url.Values{}
	q.Set("_txlock", txlock)
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	return sql.Open("sqlite", "file:"+path+"?"+q.Encode())
}

func (s *SQLite) Close() error { return errors.Join(s.r.Close(), s.w.Close()) }

type bodies struct {
	RequestObject  json.RawMessage   `json:"requestObject,omitempty"`
	ResponseObject json.RawMessage   `json:"responseObject,omitempty"`
	ResponseStatus json.RawMessage   `json:"responseStatus,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

func (s *SQLite) Insert(ctx context.Context, rows []Row) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO events (id, ts, source, lane, noise, write, uid, event, body) VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	for _, r := range rows {
		e := r.Detail.Event
		ev, err := json.Marshal(e)
		if err != nil {
			return err
		}
		var body []byte
		if r.KeepBody {
			d := r.Detail
			b := bodies{d.RequestObject, d.ResponseObject, d.ResponseStatus, d.Annotations}
			raw, err := json.Marshal(b)
			if err != nil {
				return err
			}
			gz.Reset()
			zw.Reset(&gz)
			zw.Write(raw)
			zw.Close()
			body = bytes.Clone(gz.Bytes())
		}
		if _, err := stmt.ExecContext(ctx, e.ID, e.Time.UnixMicro(), e.Source, e.From, e.Noise, e.Write, e.UID, ev, body); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLite) Get(ctx context.Context, id uint64) (model.Detail, bool, error) {
	ctx = readContext(ctx)
	var ev, body []byte
	err := s.r.QueryRowContext(ctx, `SELECT event, body FROM events WHERE id = ?`, id).Scan(&ev, &body)
	if err == sql.ErrNoRows {
		return model.Detail{}, false, nil
	}
	if err != nil {
		return model.Detail{}, false, err
	}
	var d model.Detail
	if err := json.Unmarshal(ev, &d.Event); err != nil {
		return d, false, err
	}
	if len(body) > 0 {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return d, false, err
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			return d, false, err
		}
		var b bodies
		if err := json.Unmarshal(raw, &b); err != nil {
			return d, false, err
		}
		d.RequestObject, d.ResponseObject, d.ResponseStatus, d.Annotations = b.RequestObject, b.ResponseObject, b.ResponseStatus, b.Annotations
	}
	return d, true, nil
}

func (s *SQLite) MaxID(ctx context.Context) (uint64, error) {
	ctx = readContext(ctx)
	var id sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT MAX(id) FROM events`).Scan(&id)
	return uint64(id.Int64), err
}

func (s *SQLite) Recent(ctx context.Context, n int) ([]model.Event, error) {
	ctx = readContext(ctx)
	rows, err := s.r.QueryContext(ctx,
		`SELECT event FROM (SELECT id, event FROM events ORDER BY id DESC LIMIT ?) ORDER BY id`, n)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

func (s *SQLite) Range(ctx context.Context, q Query) (RangeResult, error) {
	ctx = readContext(ctx)
	where, args := rangeFilter(q)
	var res RangeResult
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE `+where, args...).Scan(&res.Total); err != nil {
		return res, err
	}
	rows, err := s.r.QueryContext(ctx, `SELECT event FROM events WHERE `+where+` ORDER BY id LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return res, err
	}
	if res.Events, err = scanEvents(rows); err != nil {
		return res, err
	}
	res.Truncated = res.Total > len(res.Events)

	res.NoiseCounts = map[string]int{}
	nrows, err := s.r.QueryContext(ctx,
		`SELECT noise, COUNT(*) FROM events WHERE ts >= ? AND ts < ? AND noise != '' GROUP BY noise`,
		q.From.UnixMicro(), q.To.UnixMicro())
	if err != nil {
		return res, err
	}
	defer nrows.Close()
	for nrows.Next() {
		var n string
		var c int
		if err := nrows.Scan(&n, &c); err != nil {
			return res, err
		}
		res.NoiseCounts[n] = c
	}
	return res, nrows.Err()
}

func rangeFilter(q Query) (string, []any) {
	where := []string{"ts >= ?", "ts < ?"}
	args := []any{q.From.UnixMicro(), q.To.UnixMicro()}
	if len(q.HideNoise) > 0 {
		where = append(where, "noise NOT IN (?"+strings.Repeat(",?", len(q.HideNoise)-1)+")")
		for _, n := range q.HideNoise {
			args = append(args, n)
		}
	}
	return strings.Join(where, " AND "), args
}

func (s *SQLite) Histogram(ctx context.Context, from, to time.Time, n int) (Histogram, error) {
	ctx = readContext(ctx)
	width := max(to.Sub(from).Microseconds()/int64(n), 1)
	h := Histogram{From: from, To: to, BucketMs: width / 1000, Buckets: make([]Bucket, n), Markers: []model.Event{}}
	for i := range h.Buckets {
		h.Buckets[i].Time = from.Add(time.Duration(int64(i)*width) * time.Microsecond)
	}
	rows, err := s.r.QueryContext(ctx, `
		SELECT (ts - ?) / ? AS b, COUNT(*), SUM(noise = ''), SUM(noise = '' AND write = 1)
		FROM events WHERE ts >= ? AND ts < ? GROUP BY b`,
		from.UnixMicro(), width, from.UnixMicro(), to.UnixMicro())
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var b int64
		var total, shown, writes int
		if err := rows.Scan(&b, &total, &shown, &writes); err != nil {
			return h, err
		}
		// The range rarely divides evenly; the last partial slice belongs to the last bucket.
		b = min(max(b, 0), int64(n-1))
		h.Buckets[b].Total += total
		h.Buckets[b].Shown += shown
		h.Buckets[b].Writes += writes
	}
	if err := rows.Err(); err != nil {
		return h, err
	}

	mrows, err := s.r.QueryContext(ctx,
		`SELECT event FROM events WHERE source = 'marker' AND ts >= ? AND ts < ? ORDER BY id LIMIT 500`,
		from.UnixMicro(), to.UnixMicro())
	if err != nil {
		return h, err
	}
	if h.Markers, err = scanEvents(mrows); err != nil {
		return h, err
	}
	return h, nil
}

func (s *SQLite) Info(ctx context.Context) (Info, error) {
	ctx = readContext(ctx)
	var info Info
	var oldest, newest sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*), MIN(ts), MAX(ts) FROM events`).Scan(&info.Count, &oldest, &newest)
	if err != nil {
		return info, err
	}
	if oldest.Valid {
		o, n := time.UnixMicro(oldest.Int64), time.UnixMicro(newest.Int64)
		info.Oldest, info.Newest = &o, &n
	}
	info.Bytes, err = size(ctx, s.r)
	return info, err
}

// size is the space used by live pages (free pages waiting for vacuum excluded).
func size(ctx context.Context, db *sql.DB) (int64, error) {
	var pages, free, pageSize int64
	err := db.QueryRowContext(ctx,
		`SELECT p.page_count, f.freelist_count, s.page_size FROM pragma_page_count p, pragma_freelist_count f, pragma_page_size s`).
		Scan(&pages, &free, &pageSize)
	return (pages - free) * pageSize, err
}

func (s *SQLite) Prune(ctx context.Context, cutoff time.Time, maxBytes int64) (byAge, bySize int64, err error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM events WHERE ts < ?`, cutoff.UnixMicro())
	if err != nil {
		return 0, 0, err
	}
	byAge, _ = res.RowsAffected()

	// Over the size limit: delete the oldest events in proportion to the overshoot,
	// aiming 10% below the limit so this doesn't run again on the next insert.
	// Space is not freed exactly in proportion (pages, indexes), hence the few rounds.
	for i := 0; maxBytes > 0 && i < 5; i++ {
		size, err := size(ctx, s.w)
		if err != nil || size <= maxBytes {
			break
		}
		var count int
		if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&count); err != nil || count == 0 {
			break
		}
		drop := max(int(float64(count)*(1-0.9*float64(maxBytes)/float64(size))), 1)
		res, err := s.w.ExecContext(ctx, `
			DELETE FROM events WHERE id <= (SELECT id FROM events ORDER BY id LIMIT 1 OFFSET ?)`, drop-1)
		if err != nil {
			return byAge, bySize, err
		}
		n, _ := res.RowsAffected()
		bySize += n
		if n == 0 {
			break
		}
	}
	if byAge+bySize > 0 {
		if err := s.vacuum(ctx); err != nil {
			return byAge, bySize, err
		}
	}
	// Copy the write-ahead log into the main file. PASSIVE never waits for readers
	// and never blocks writes (TRUNCATE does both); journal_size_limit trims the file.
	return byAge, bySize, s.checkpoint(ctx)
}

func (s *SQLite) checkpoint(ctx context.Context) error {
	rows, err := s.w.QueryContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

// vacuum gives free pages back to the file system. incremental_vacuum frees
// pages one step at a time, so it must be read to the end like a query:
// a plain Exec runs only the first step and frees almost nothing.
func (s *SQLite) vacuum(ctx context.Context) error {
	rows, err := s.w.QueryContext(ctx, `PRAGMA incremental_vacuum`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

func scanEvents(rows *sql.Rows) ([]model.Event, error) {
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var e model.Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
