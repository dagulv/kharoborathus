package categories

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

type Store interface {
	EnabledCategories(ctx context.Context) ([]domain.Category, error)
}

// The public API, so the web app can list categories to filter posts by.
func Routes(mux *http.ServeMux, s Store, l *slog.Logger) {
	mux.HandleFunc("GET /categories", listCategories(s, l))
}

func listCategories(s Store, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cats, err := s.EnabledCategories(r.Context())
		if err != nil {
			l.Error("list categories", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if cats == nil {
			cats = []domain.Category{}
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(cats); err != nil {
			l.Error("write response", "err", err)
		}
	}
}
