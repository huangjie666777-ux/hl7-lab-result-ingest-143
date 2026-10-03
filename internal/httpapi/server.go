// Package httpapi exposes the read-only query API over HTTP.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"example.com/hl7-lab-result-ingest/internal/store"
)

// NewRouter builds the chi router for the query API.
func NewRouter(st *store.Store) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * 1e9))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/api/results", func(w http.ResponseWriter, req *http.Request) {
		source, order, ok := params(w, req)
		if !ok {
			return
		}
		patient, results, err := st.CurrentResults(req.Context(), source, order)
		respond(w, patient, results, err)
	})
	r.Get("/api/results/history", func(w http.ResponseWriter, req *http.Request) {
		source, order, ok := params(w, req)
		if !ok {
			return
		}
		patient, entries, err := st.History(req.Context(), source, order)
		respond(w, patient, entries, err)
	})
	return r
}

func params(w http.ResponseWriter, req *http.Request) (string, string, bool) {
	q := req.URL.Query()
	source, order := q.Get("source"), q.Get("order")
	if source == "" || order == "" {
		http.Error(w, "source and order query parameters are required", http.StatusBadRequest)
		return "", "", false
	}
	return source, order, true
}

func respond(w http.ResponseWriter, patient string, payload any, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"patient_id": patient,
			"data":       payload,
		})
	}
}
