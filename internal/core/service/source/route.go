package source

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	fetchers "github.com/dagulv/kharoborathus/internal/adapter/fetcher"
	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	SyncQueue       = 16
	discoverTimeout = time.Minute
	homepageTimeout = 20 * time.Second

	invalidTextRepresentation = "22P02"
)

type listing struct {
	Candidates []Candidate         `json:"candidates"`
	WordPress  *fetchers.WordPress `json:"wordpress,omitempty"`
}

// Served on the internal port, which only the admin reaches.
func Routes(mux *http.ServeMux, s *postgres.SourceStore, syncs chan<- domain.Source, l, calls *slog.Logger) {
	mux.HandleFunc("POST /sources/{id}/sync", syncSource(s, syncs, l))
	mux.HandleFunc("GET /listing", listingRoute(safehttp.NewClient(homepageTimeout), l, calls))
}

// listingRoute lists what the admin can add a url as, best first.
func listingRoute(client *http.Client, l, calls *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := r.URL.Query().Get("url")
		if u == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}

		// Probing can take several requests, and the admin waits for all of them.
		ctx, cancel := context.WithTimeout(r.Context(), discoverTimeout)
		defer cancel()

		c := logs.Client(client, calls.With("url", u), "homepage")

		candidates, err := discover(ctx, c, u)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		out := listing{Candidates: candidates}

		// Informs the admin only. The backfill checks the chosen listing itself once the source is scraped.
		wp, err := fetchers.FindWordPress(ctx, c, nil, u)
		if err != nil {
			l.Warn("find wordpress", "url", u, "err", err)
		}

		if wp.Posts != "" {
			out.WordPress = &wp
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(out); err != nil {
			l.Error("write listing", "url", u, "err", err)
		}
	}
}

func syncSource(s *postgres.SourceStore, syncs chan<- domain.Source, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source, err := s.GetSource(r.Context(), r.PathValue("id"))

		var pgErr *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == invalidTextRepresentation) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		if err != nil {
			l.Error("sync source", "source", r.PathValue("id"), "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if source.Status != domain.StatusActive || !source.Enabled {
			http.Error(w, "source not active or disabled", http.StatusConflict)
			return
		}

		select {
		case syncs <- source:
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, "sync queue full", http.StatusServiceUnavailable)
		}
	}
}
