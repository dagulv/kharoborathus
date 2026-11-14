package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPostRoutes(t *testing.T) {
	mux, _ := newMux(t)

	cases := []struct {
		path string
		want int
	}{
		{"/admin/posts", http.StatusOK},
		{"/admin/posts?page=2&search=" + url.QueryEscape(`"`), http.StatusOK},
		{"/admin/posts?source=nope", http.StatusBadRequest},
		{"/admin/posts?page=0", http.StatusBadRequest},
		{"/admin/posts?page=x", http.StatusBadRequest},
		{"/admin/posts?tag=" + strings.Repeat("a", 201), http.StatusBadRequest},
		{"/admin/posts?search=" + strings.Repeat("a", 501), http.StatusBadRequest},
		{"/admin/posts/00000000-0000-0000-0000-000000000000", http.StatusNotFound},
		{"/admin/posts/x", http.StatusNotFound},
	}

	for _, c := range cases {
		if rec := do(mux, http.MethodGet, c.path, ""); rec.Code != c.want {
			t.Errorf("%s: %d %s", c.path, rec.Code, rec.Body)
		}
	}

	rec := do(mux, http.MethodGet, "/admin/posts", "")

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if posts, ok := got["posts"].([]any); !ok || len(posts) != 0 || got["total"] != 0.0 || got["page"] != 1.0 || got["pageSize"] != 50.0 {
		t.Errorf("empty list: %s", rec.Body)
	}
}
