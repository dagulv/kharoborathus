package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// An empty page means the first. Anything else must be an integer of at least 1.
func parsePage(raw string) (int, bool) {
	if raw == "" {
		return 1, true
	}

	page, err := strconv.Atoi(raw)

	return page, err == nil && page >= 1
}

func listPosts(s *postgres.PostStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		f := domain.AdminPostFilter{
			SourceID: q.Get("source"),
			Tag:      q.Get("tag"),
			Search:   strings.TrimSpace(q.Get("search")),
		}

		if f.SourceID != "" && uuid.Validate(f.SourceID) != nil {
			http.Error(w, "source must be a uuid", http.StatusBadRequest)
			return
		}

		if len(f.Tag) > 200 || len(f.Search) > 500 {
			http.Error(w, "tag or search too long", http.StatusBadRequest)
			return
		}

		page, ok := parsePage(q.Get("page"))
		if !ok {
			http.Error(w, "page must be an integer of at least 1", http.StatusBadRequest)
			return
		}

		f.Page = page

		posts, err := s.ListAdminPosts(r.Context(), f)
		if err != nil {
			serverError(w, l, "list posts", err)
			return
		}

		writeJSON(w, l, posts)
	}
}

func getPost(s *postgres.PostStore, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		post, err := s.GetAdminPost(r.Context(), r.PathValue("id"))
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Post not found", http.StatusNotFound)
			return
		}

		if err != nil {
			serverError(w, l, "get post", err)
			return
		}

		writeJSON(w, l, post)
	}
}
