package entities

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/query"
	"github.com/jackc/pgx/v5"
)

type Store interface {
	ListPublic(ctx context.Context, f domain.EntityFilter) ([]domain.PublicEntity, error)
	GetPublic(ctx context.Context, key string) (domain.PublicEntity, error)
}

// The public API, so the web app can list entities to filter posts by.
func Routes(mux *http.ServeMux, s Store, l *slog.Logger) {
	mux.HandleFunc("GET /entities", listEntities(s, l))
	mux.HandleFunc("GET /entities/{key}", getEntity(s, l))
}

func listEntities(s Store, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		entities, err := s.ListPublic(r.Context(), domain.EntityFilter{
			Filter: query.Filter{
				Limit:  q.Get("limit"),
				Offset: q.Get("offset"),
			},
			Kind:     q.Get("kind"),
			Tag:      q.Get("tag"),
			Category: q.Get("category"),
		})
		if err != nil {
			l.Error("list entities", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if entities == nil {
			entities = []domain.PublicEntity{}
		}

		writeJSON(w, l, entities)
	}
}

func getEntity(s Store, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, err := s.GetPublic(r.Context(), r.PathValue("key"))
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		if err != nil {
			l.Error("get entity", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		writeJSON(w, l, entity)
	}
}

func writeJSON(w http.ResponseWriter, l *slog.Logger, v any) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(v); err != nil {
		l.Error("write response", "err", err)
	}
}
