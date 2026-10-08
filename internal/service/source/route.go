package source

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

const SyncQueue = 16

const invalidTextRepresentation = "22P02"

func Routes(mux *http.ServeMux, s *Store, syncs chan<- Source, l *slog.Logger) {
	mux.HandleFunc("POST /sources/{id}/sync", syncSource(s, syncs, l))
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

		select {
		case syncs <- source:
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, "sync queue full", http.StatusServiceUnavailable)
		}
	}
}
