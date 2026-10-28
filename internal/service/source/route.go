package source

import (
	"context"
	"database/sql"
	json "encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/service/source/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/model"
	"github.com/jackc/pgx/v5/pgconn"
)

const SyncQueue = 16

const discoverTimeout = time.Minute

const invalidTextRepresentation = "22P02"

type listing struct {
	Candidates []Candidate       `json:"candidates"`
	WordPress  adapter.WordPress `json:"wordpress,omitzero"`
}

func Routes(mux *http.ServeMux, s *Store, syncs chan<- model.Source, l, calls *slog.Logger) {
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

		// Informs the admin only. The backfill checks the chosen listing itself once the source is scraped.
		wp, err := adapter.FindWordPress(ctx, c, nil, u)
		if err != nil {
			l.Warn("find wordpress", "url", u, "err", err)
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.MarshalWrite(w, listing{Candidates: candidates, WordPress: wp}); err != nil {
			l.Error("write listing", "url", u, "err", err)
		}
	}
}

func syncSource(s *Store, syncs chan<- model.Source, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source, err := s.GetSource(r.Context(), r.PathValue("id"))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.Is(err, sql.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == invalidTextRepresentation) {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
				return
			}

			l.Error("sync source", "source", r.PathValue("id"), "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if source.Status != model.StatusActive || !source.Enabled {
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
