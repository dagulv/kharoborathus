package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

const maxCategoryTerm = 100

func validCategoryTerm(term string) bool {
	return term != "" && utf8.RuneCountInString(term) <= maxCategoryTerm
}

func listCategories(s *postgres.CategoryStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cats, err := s.ListAdminCategories(r.Context())
		if err != nil {
			serverError(w, l, "list categories", err)
			return
		}

		writeJSON(w, l, cats)
	}
}

func createCategory(s *postgres.CategoryStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in domain.NewCategory
		if err := readJSON(w, r, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		in.Key = discovery.TagKey(in.Key)
		in.Sv = strings.TrimSpace(in.Sv)
		in.En = strings.TrimSpace(in.En)

		if !validTagKey(in.Key) {
			http.Error(w, "invalid category key", http.StatusBadRequest)
			return
		}

		if !validCategoryTerm(in.Sv) || !validCategoryTerm(in.En) {
			http.Error(w, "sv and en are required, at most 100 characters", http.StatusBadRequest)
			return
		}

		err := s.CreateCategory(r.Context(), in)
		if errors.Is(err, domain.ErrCategoryExists) {
			http.Error(w, domain.ErrCategoryExists.Error(), http.StatusConflict)
			return
		}

		if err != nil {
			serverError(w, l, "create category", err)
			return
		}

		w.WriteHeader(http.StatusCreated)
	}
}

func setCategoryEnabled(s *postgres.CategoryStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if !validTagKey(key) {
			http.Error(w, "invalid category key", http.StatusBadRequest)
			return
		}

		var in struct {
			Enabled bool `json:"enabled"`
		}

		if err := readJSON(w, r, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		err := s.SetCategoryEnabled(r.Context(), key, in.Enabled)
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, domain.ErrUnknownCategory.Error(), http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "set category enabled", err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func setTagCategories(s *postgres.CategoryStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if !validTagKey(key) {
			http.Error(w, "invalid tag key", http.StatusBadRequest)
			return
		}

		var in struct {
			Categories []string `json:"categories"`
		}

		if err := readJSON(w, r, &in); err != nil || in.Categories == nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		for i, c := range in.Categories {
			in.Categories[i] = discovery.TagKey(c)
		}

		err := s.SetTagCategories(r.Context(), key, in.Categories)
		if errors.Is(err, domain.ErrUnknownCategory) {
			http.Error(w, domain.ErrUnknownCategory.Error(), http.StatusBadRequest)
			return
		}

		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Unknown tag "+key, http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "set tag categories", err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
