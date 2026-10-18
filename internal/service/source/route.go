package source

import (
	"database/sql"
	json "encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/service/source/adapter"
	"github.com/jackc/pgx/v5/pgconn"
)

const SyncQueue = 16

const invalidTextRepresentation = "22P02"

func Routes(mux *http.ServeMux, s *Store, syncs chan<- Source, l, calls *slog.Logger) {
	mux.HandleFunc("POST /sources/{id}/sync", syncSource(s, syncs, l))
	mux.HandleFunc("GET /listing", listingRoute(safehttp.NewClient(homepageTimeout), l, calls))
}

// listingRoute lets the admin swap a new page source for the feed its page links to.
func listingRoute(client *http.Client, l, calls *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := r.URL.Query().Get("url")
		if u == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}

		page, final, err := adapter.FetchPage(r.Context(), logs.Client(client, calls.With("url", u), "homepage"), u)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		name, listing := findListing(page, final, u)

		w.Header().Set("Content-Type", "application/json")

		if err := json.MarshalWrite(w, map[string]string{"adapter": name, "url": listing}); err != nil {
			l.Error("write listing", "url", u, "err", err)
		}
	}
}

func syncSource(s *Store, syncs chan<- Source, l *slog.Logger) http.HandlerFunc {
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

		if source.Status != StatusActive || !source.Enabled {
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
