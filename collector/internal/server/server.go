// Package server exposes the audit webhook, the browser API and the UI.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"

	"kubescope/internal/audit"
	"kubescope/internal/model"
	"kubescope/internal/store"
)

// dist is filled by `make web` (vite builds into this folder).
//
//go:embed all:dist
var dist embed.FS

const (
	backlog  = 5000   // events sent when a browser connects in live mode
	maxRange = 50_000 // most events returned for one time range
)

type Server struct {
	store *store.Store
}

func New(s *store.Store) *Server { return &Server{store: s} }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /audit", s.handleAudit)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /api/events", s.handleRange)
	mux.HandleFunc("GET /api/events/{id}", s.handleEvent)
	mux.HandleFunc("GET /api/histogram", s.handleHistogram)
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("POST /api/marker", s.handleMarker)
	mux.Handle("/", ui())
	return mux
}

// handleAudit receives batches from the API server audit webhook.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	var list auditv1.EventList
	if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
		log.Printf("audit: bad payload: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	details := make([]model.Detail, 0, len(list.Items))
	for i := range list.Items {
		if d, ok := audit.Convert(&list.Items[i]); ok {
			details = append(details, d)
		}
	}
	s.store.Add(details)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	d, ok, err := s.store.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "event not in history: noise that is shown live only (see -store-noise), or deleted by retention", http.StatusNotFound)
		return
	}
	sendJSON(w, d)
}

// handleRange returns stored events in [from, to), e.g.
// /api/events?from=1729800000000&to=1729800600000&hide=lease,watch (times in unix ms).
func (s *Server) handleRange(w http.ResponseWriter, r *http.Request) {
	from, to, ok := timeRange(w, r)
	if !ok {
		return
	}
	limit := maxRange
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n < maxRange {
		limit = n
	}
	var hide []string
	if h := r.URL.Query().Get("hide"); h != "" {
		hide = strings.Split(h, ",")
	}
	res, err := s.store.Range(r.Context(), store.Query{From: from, To: to, HideNoise: hide, Limit: limit})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sendJSON(w, res)
}

func (s *Server) handleHistogram(w http.ResponseWriter, r *http.Request) {
	from, to, ok := timeRange(w, r)
	if !ok {
		return
	}
	buckets := 120
	if n, err := strconv.Atoi(r.URL.Query().Get("buckets")); err == nil && n > 0 && n <= 1000 {
		buckets = n
	}
	h, err := s.store.Histogram(r.Context(), from, to, buckets)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sendJSON(w, h)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.store.Info(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sendJSON(w, info)
}

func timeRange(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	f, err1 := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	t, err2 := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
	if err1 != nil || err2 != nil || t <= f {
		http.Error(w, "from and to are required (unix ms, from < to)", http.StatusBadRequest)
		return from, to, false
	}
	return time.UnixMilli(f), time.UnixMilli(t), true
}

func sendJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleMarker adds a labelled divider, e.g. `curl -d '{"label":"Step 1"}' localhost:8090/api/marker`.
func (s *Server) handleMarker(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Label == "" {
		http.Error(w, `expected {"label": "..."}`, http.StatusBadRequest)
		return
	}
	log.Printf("marker: %s", body.Label)
	s.store.Add([]model.Detail{{Event: model.Event{
		Time:    time.Now(),
		Source:  model.SourceMarker,
		Verb:    model.SourceMarker,
		Summary: body.Label,
	}}})
	w.WriteHeader(http.StatusNoContent)
}

// handleWS sends the backlog, then streams new events until the browser leaves.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow the Vite dev server (another port) to connect.
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*"},
	})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())

	events, ch, err := s.store.Subscribe(ctx, backlog)
	if err != nil {
		c.Close(websocket.StatusInternalError, "could not read history")
		return
	}
	defer s.store.Unsubscribe(ch)

	if err := writeJSON(ctx, c, store.Message{Type: "backlog", Events: events}); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				c.Close(websocket.StatusTryAgainLater, "too slow, reconnect")
				return
			}
			if err := writeJSON(ctx, c, m); err != nil {
				return
			}
		}
	}
}

func writeJSON(ctx context.Context, c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, b)
}

func ui() http.Handler {
	sub, _ := fs.Sub(dist, "dist")
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "UI not built. Run `make web` (or use the Vite dev server on :5173).", http.StatusNotFound)
		})
	}
	return http.FileServerFS(sub)
}
