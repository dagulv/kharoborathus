package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTagRoutes(t *testing.T) {
	mux, db := newMux(t)

	if _, err := db.Exec(context.Background(), `INSERT INTO tags (key) VALUES ('a'), ('b')`); err != nil {
		t.Fatal(err)
	}

	rec := do(mux, http.MethodGet, "/admin/tags", "")

	var got map[string][]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got["tags"]) != 2 || got["proposals"] == nil || got["aliases"] == nil {
		t.Fatalf("overview: %d %s", rec.Code, rec.Body)
	}

	cases := []struct {
		method, path, body string
		want               int
		message            string
	}{
		{http.MethodPost, "/admin/tags/merge", `{"from":"a","to":"a"}`, http.StatusBadRequest, "Cannot merge a tag into itself"},
		{http.MethodPost, "/admin/tags/merge", `{"from":"a","to":"zz"}`, http.StatusNotFound, "Unknown tag zz"},
		{http.MethodPost, "/admin/tags/merge", `{"from":"","to":"a"}`, http.StatusBadRequest, ""},
		{http.MethodPatch, "/admin/tags/" + strings.Repeat("k", 41), `{"enabled":true}`, http.StatusBadRequest, ""},
		{http.MethodPost, "/admin/tags/merge", `{"from":"a","to":"b"}`, http.StatusNoContent, ""},
	}

	for _, c := range cases {
		rec := do(mux, c.method, c.path, c.body)
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.message) {
			t.Errorf("%s %s %s: %d %s", c.method, c.path, c.body, rec.Code, rec.Body)
		}
	}
}

func TestTagRoutesEscapedKey(t *testing.T) {
	mux, db := newMux(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `INSERT INTO tags (key) VALUES ('a/b'), ('a b'), ('a')`); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ path, key string }{{"/admin/tags/a%2Fb", "a/b"}, {"/admin/tags/a%20b", "a b"}} {
		if rec := do(mux, http.MethodPatch, c.path, `{"enabled":false}`); rec.Code != http.StatusNoContent {
			t.Fatalf("%s: %d %s", c.path, rec.Code, rec.Body)
		}

		var enabled bool
		if err := db.QueryRow(ctx, `SELECT enabled FROM tags WHERE key = $1`, c.key).Scan(&enabled); err != nil || enabled {
			t.Errorf("%s: enabled %v, %v", c.key, enabled, err)
		}
	}

	var enabled bool
	if err := db.QueryRow(ctx, `SELECT enabled FROM tags WHERE key = 'a'`).Scan(&enabled); err != nil || !enabled {
		t.Errorf("a: enabled %v, %v", enabled, err)
	}
}
