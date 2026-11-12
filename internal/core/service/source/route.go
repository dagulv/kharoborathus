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
)

const (
	SyncQueue       = 16
	BackfillQueue   = 16
	discoverTimeout = time.Minute
	homepageTimeout = 20 * time.Second
)

type listing struct {
	Candidates []Candidate         `json:"candidates"`
	WordPress  *fetchers.WordPress `json:"wordpress,omitempty"`
}

// The admin group. main guards /admin/ with the session middleware.
func Routes(mux *http.ServeMux, s *postgres.SourceStore, syncs, backfills chan<- domain.Source, l, calls *slog.Logger) {
	mux.HandleFunc("POST /admin/sources/{id}/sync", queueSource(s, syncs, "sync", l))
	mux.HandleFunc("POST /admin/sources/{id}/backfill", queueSource(s, backfills, "backfill", l))
	mux.HandleFunc("GET /admin/listing", listingRoute(safehttp.NewClient(homepageTimeout), l, calls))
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

func queueSource(s *postgres.SourceStore, queue chan<- domain.Source, name string, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source, err := s.GetSource(r.Context(), r.PathValue("id"))
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		if err != nil {
			l.Error(name+" source", "source", r.PathValue("id"), "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if source.Status != domain.StatusActive || !source.Enabled {
			http.Error(w, "source not active or disabled", http.StatusConflict)
			return
		}

		select {
		case queue <- source:
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, name+" queue full", http.StatusServiceUnavailable)
		}
	}
}
