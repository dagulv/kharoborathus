package admin

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

func validTagKey(key string) bool {
	return key != "" && len(key) <= 40
}

func getTags(s *postgres.TagStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o, err := s.TagOverview(r.Context())
		if err != nil {
			serverError(w, l, "tag overview", err)
			return
		}

		writeJSON(w, l, o)
	}
}

func setTagEnabled(s *postgres.TagStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if !validTagKey(key) {
			http.Error(w, "invalid tag key", http.StatusBadRequest)
			return
		}

		var in struct {
			Enabled bool `json:"enabled"`
		}

		if err := readJSON(w, r, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		if err := s.SetTagEnabled(r.Context(), key, in.Enabled); err != nil {
			serverError(w, l, "set tag enabled", err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func mergeTag(s *postgres.TagStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			From string `json:"from"`
			To   string `json:"to"`
		}

		if err := readJSON(w, r, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		if !validTagKey(in.From) || !validTagKey(in.To) {
			http.Error(w, "invalid tag key", http.StatusBadRequest)
			return
		}

		err := s.MergeTag(r.Context(), in.From, in.To)
		if errors.Is(err, domain.ErrMergeSelf) {
			http.Error(w, "Cannot merge a tag into itself", http.StatusBadRequest)
			return
		}

		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Unknown tag "+in.To, http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "merge tag", err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
