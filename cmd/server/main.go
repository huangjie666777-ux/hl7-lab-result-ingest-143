// Command server runs the HL7 lab result archive: MLLP ingest + HTTP query.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/hl7-lab-result-ingest/internal/httpapi"
	"example.com/hl7-lab-result-ingest/internal/mllp"
	"example.com/hl7-lab-result-ingest/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	mllpAddr := env("MLLP_ADDR", ":2575")
	httpAddr := env("HTTP_ADDR", ":8080")
	dbPath := env("DB_PATH", "lab.db")

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	mllpSrv := mllp.New(mllpAddr, st, 64, 30*time.Second)
	go func() {
		if err := mllpSrv.ListenAndServe(); err != nil {
			log.Fatalf("mllp server: %v", err)
		}
	}()

	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           httpapi.NewRouter(st),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Printf("http: listening on %s", httpAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")

	mllpSrv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
