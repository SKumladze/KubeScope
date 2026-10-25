// KubeScope collector: receives Kubernetes audit events, stores them and streams them to the UI.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kubescope/internal/server"
	"kubescope/internal/store"
)

func main() {
	addr := flag.String("addr", ":8090", "listen address (the API server must reach it, so keep it on all interfaces)")
	dbPath := flag.String("db", "kubescope.db", "SQLite database file")
	retention := flag.Duration("retention", 7*24*time.Hour, "delete events older than this (0 = keep forever)")
	maxMB := flag.Int64("max-db-mb", 2048, "delete the oldest events when the database grows past this size in MB (0 = no limit)")
	noise := flag.String("store-noise", "smart", "how to store noise in history: smart = drop leases/health/discovery/self-checks, "+
		"keep the rest without bodies; meta = all noise without bodies; full; none")
	flag.Parse()

	policy := store.NoisePolicy(*noise)
	if !policy.Valid() {
		log.Fatalf("-store-noise must be smart, meta, full or none")
	}

	db, err := store.OpenSQLite(*dbPath)
	if err != nil {
		log.Fatalf("open %s: %v", *dbPath, err)
	}
	st, err := store.New(db, store.Options{Noise: policy, Retention: *retention, MaxBytes: *maxMB << 20})
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	srv := &http.Server{Addr: *addr, Handler: server.New(st).Handler()}
	go func() {
		log.Printf("KubeScope listening on %s, open http://localhost%s (history in %s)", *addr, *addr, *dbPath)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	// On Ctrl+C, flush queued events to disk before exiting.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	if err := st.Close(); err != nil {
		log.Printf("close: %v", err)
	}
	log.Print("stopped")
}
