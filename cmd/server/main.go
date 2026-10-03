package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/hl7-lab-result-ingest/internal/httpapi"
	"example.com/hl7-lab-result-ingest/internal/ingest"
	"example.com/hl7-lab-result-ingest/internal/mllp"
	"example.com/hl7-lab-result-ingest/internal/store"
)

func main() {
	var (
		mllpAddr = flag.String("mllp-addr", ":2575", "MLLP/TCP listen address")
		httpAddr = flag.String("http-addr", ":8080", "HTTP query API listen address")
		dbPath   = flag.String("db", "data/lab.db", "SQLite database path")
		maxConns = flag.Int("max-conns", 32, "max concurrent MLLP connections")
	)
	flag.Parse()

	if err := os.MkdirAll(dirOf(*dbPath), 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := ingest.New(st)
	mllpSrv := &mllp.Server{
		Addr:     *mllpAddr,
		MaxConns: *maxConns,
		Handler: func(raw string) string {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return svc.HandleFrame(ctx, raw)
		},
	}
	httpSrv := &http.Server{
		Addr:              *httpAddr,
		Handler:           httpapi.NewRouter(st),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- mllpSrv.ListenAndServe() }()
	go func() {
		log.Printf("http: listening on %s", *httpAddr)
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-sig:
		log.Printf("shutting down on %s", s)
	case err := <-errCh:
		if err != nil {
			log.Printf("server error: %v", err)
		}
	}

	mllpSrv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
	log.Print("bye")
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
