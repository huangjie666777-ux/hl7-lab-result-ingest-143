// Package httpapi exposes the read-only query API.
package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"example.com/hl7-lab-result-ingest/internal/store"
)

func NewRouter(st *store.Store) http.Handler {
	r := chi.NewRouter()
	r.Use(recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	r.Route("/api/sources/{source}/orders/{order}", func(r chi.Router) {
		r.Get("/results", queryHandler(st.Current))
		r.Get("/history", queryHandler(st.History))
	})
	return http.TimeoutHandler(r, 10*time.Second, "request timeout")
}

type queryFunc func(ctx context.Context, source, order string) (*store.OrderView, error)

func queryHandler(fn queryFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		source := chi.URLParam(req, "source")
		order := chi.URLParam(req, "order")
		view, err := fn(req.Context(), source, order)
		if err != nil {
			log.Printf("httpapi: query failed: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if view == nil {
			http.Error(w, "order not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(view)
	}
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("httpapi: panic: %v", rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
