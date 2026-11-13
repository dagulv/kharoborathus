package source

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/jackc/pgx/v5"
)

const (
	SyncQueue     = 16
	BackfillQueue = 16
	RuledQueue    = 16
)

// The admin group. main guards /admin/ with the session middleware.
func Routes(mux *http.ServeMux, s *postgres.SourceStore, syncs, backfills chan<- domain.Source, prober port.Prober, l *slog.Logger) {
	mux.HandleFunc("POST /admin/sources/{id}/sync", queueSource(s, syncs, "sync", l))
	mux.HandleFunc("POST /admin/sources/{id}/backfill", queueSource(s, backfills, "backfill", l))
	mux.HandleFunc("GET /admin/listing", listingRoute(prober, l))
}

// listingRoute lists what the admin can add a url as, best first.
func listingRoute(prober port.Prober, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := r.URL.Query().Get("url")
		if u == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}

		listing, err := prober.Probe(r.Context(), u)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(listing); err != nil {
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
