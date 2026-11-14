// Package admin serves the admin SPA's reads and writes. main puts it behind the session cookie.
package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
)

type Stores struct {
	Stats   *postgres.StatsStore
	Sources *postgres.SourceStore
	Posts   *postgres.PostStore
}

func Routes(mux *http.ServeMux, s Stores, l *slog.Logger) {
	mux.HandleFunc("GET /admin/stats", getStats(s.Stats, l))
	mux.HandleFunc("GET /admin/sources", listSources(s.Sources, l))
	mux.HandleFunc("POST /admin/sources", createSource(s.Sources, l))
	mux.HandleFunc("POST /admin/sources/{id}/review", reviewSource(s.Sources, l))
	mux.HandleFunc("GET /admin/posts", listPosts(s.Posts, l))
	mux.HandleFunc("GET /admin/posts/{id}", getPost(s.Posts, l))
}

func writeJSON(w http.ResponseWriter, l *slog.Logger, v any) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(v); err != nil {
		l.Error("write response", "err", err)
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("err decode body: %w", err)
	}

	return nil
}

func serverError(w http.ResponseWriter, l *slog.Logger, msg string, err error) {
	l.Error(msg, "err", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
