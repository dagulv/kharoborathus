package posts

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"net/http"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/iterx"
	"github.com/dagulv/kharoborathus/internal/lib/query"
	"github.com/jackc/pgx/v5"
)

type Store interface {
	ListPosts(ctx context.Context, filter domain.PostFilter) iter.Seq2[domain.Post, error]
	GetPost(ctx context.Context, id string) (domain.Post, error)
}

// Served on the public port, the only one the proxy publishes.
func Routes(mux *http.ServeMux, s Store, l *slog.Logger) {
	mux.HandleFunc("GET /posts", listPosts(s, l))
	mux.HandleFunc("GET /posts/{id}", getPost(s, l))
}

func listPosts(s Store, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		filter := domain.PostFilter{
			Filter: query.Filter{
				Limit:   q.Get("limit"),
				Offset:  q.Get("offset"),
				Order:   q.Get("order"),
				OrderBy: q.Get("orderBy"),
				Search:  q.Get("search"),
			},
			Tags:  q["tag"],
			After: q.Get("after"),
		}

		// Collected first, so a failure mid-list is a 500 and never a truncated 200.
		posts, err := iterx.Collect(s.ListPosts(r.Context(), filter))
		if errors.Is(err, domain.ErrInvalidFilter) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err != nil {
			l.Error("list posts", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if posts == nil {
			posts = []domain.Post{}
		}

		writeJSON(w, l, posts)
	}
}

func getPost(s Store, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		post, err := s.GetPost(r.Context(), r.PathValue("id"))
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		if err != nil {
			l.Error("get post", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		writeJSON(w, l, post)
	}
}

func writeJSON(w http.ResponseWriter, l *slog.Logger, v any) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(v); err != nil {
		l.Error("write response", "err", err)
	}
}
