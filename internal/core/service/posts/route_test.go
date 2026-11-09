package posts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

type fakeStore struct {
	posts  []domain.Post
	err    error
	filter domain.PostFilter
}

func (f *fakeStore) ListPosts(_ context.Context, filter domain.PostFilter) iter.Seq2[domain.Post, error] {
	f.filter = filter

	return func(yield func(domain.Post, error) bool) {
		for _, p := range f.posts {
			if !yield(p, nil) {
				return
			}
		}

		if f.err != nil {
			yield(domain.Post{}, f.err)
		}
	}
}

func (f *fakeStore) GetPost(_ context.Context, id string) (domain.Post, error) {
	if f.err != nil {
		return domain.Post{}, f.err
	}

	for _, p := range f.posts {
		if p.ID == id {
			return p, nil
		}
	}

	return domain.Post{}, fmt.Errorf("err get post %s: %w", id, pgx.ErrNoRows)
}

func serve(s Store, target string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	Routes(mux, s, slog.New(slog.DiscardHandler))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))

	return w
}

func TestListPosts(t *testing.T) {
	s := &fakeStore{posts: []domain.Post{{ID: "1", Title: "Volvo", Tags: []string{"acquisition"}}, {ID: "2", Title: "Ericsson"}}}

	w := serve(s, "/posts?limit=10&offset=5&order=asc&orderBy=createdAt&search=volvo&tag=acquisition&tag=contract&after=9")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", w.Code, w.Header().Get("Content-Type"))
	}

	var got []domain.Post
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 2 || got[0].Title != "Volvo" {
		t.Errorf("body %s, %v", w.Body, err)
	}

	f := s.filter
	if f.Limit != "10" || f.Offset != "5" || f.Order != "asc" || f.OrderBy != "createdAt" || f.Search != "volvo" || f.After != "9" || !slices.Equal(f.Tags, []string{"acquisition", "contract"}) {
		t.Errorf("filter %+v", f)
	}
}

func TestListPostsEmpty(t *testing.T) {
	if w := serve(&fakeStore{}, "/posts"); w.Code != http.StatusOK || w.Body.String() != "[]\n" {
		t.Errorf("status %d, body %q", w.Code, w.Body)
	}
}

func TestListPostsError(t *testing.T) {
	s := &fakeStore{posts: []domain.Post{{ID: "1"}}, err: errors.New("db down")}

	if w := serve(s, "/posts"); w.Code != http.StatusInternalServerError {
		t.Errorf("status %d", w.Code)
	}
}

func TestGetPost(t *testing.T) {
	s := &fakeStore{posts: []domain.Post{{ID: "1", Title: "Volvo"}}}

	w := serve(s, "/posts/1")

	var got domain.Post
	if err := json.Unmarshal(w.Body.Bytes(), &got); w.Code != http.StatusOK || err != nil || got.Title != "Volvo" {
		t.Errorf("status %d, body %s, %v", w.Code, w.Body, err)
	}

	if w := serve(s, "/posts/2"); w.Code != http.StatusNotFound {
		t.Errorf("missing: status %d", w.Code)
	}

	if w := serve(&fakeStore{err: errors.New("db down")}, "/posts/1"); w.Code != http.StatusInternalServerError {
		t.Errorf("error: status %d", w.Code)
	}
}

func TestListPostsInvalidFilter(t *testing.T) {
	s := &fakeStore{err: fmt.Errorf("after: %w", domain.ErrInvalidFilter)}

	if w := serve(s, "/posts?after=x"); w.Code != http.StatusBadRequest {
		t.Errorf("status %d", w.Code)
	}
}
