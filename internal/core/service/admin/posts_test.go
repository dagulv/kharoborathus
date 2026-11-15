package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPostRoutes(t *testing.T) {
	mux, _, _ := newMux(t)

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
		{"/admin/posts?status=rejected", http.StatusOK},
		{"/admin/posts?status=nope", http.StatusBadRequest},
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

func TestTraceHasNoBodies(t *testing.T) {
	mux, db, _ := newMux(t)
	ctx := context.Background()

	var rawPostID string
	if err := db.QueryRow(ctx, `
		WITH d AS (INSERT INTO domains (base_url) VALUES ('https://a.example') RETURNING id)
		INSERT INTO raw_posts (url, domain_id, status) SELECT 'https://a.example/1', id, 'failed' FROM d RETURNING id
		`).Scan(&rawPostID); err != nil {
		t.Fatal(err)
	}

	big := strings.Repeat("x", 1<<20)

	if _, err := db.Exec(ctx, `
		INSERT INTO logs (type, level, request, response, metadata) VALUES
			('page', 'WARN', '{"url": "https://a.example/1"}', to_jsonb($2::text), jsonb_build_object('raw_post_id', $1::text, 'step', 'fetch')),
			('step', 'WARN', '{"url": "https://a.example/1"}', '{"bytes": 0}', jsonb_build_object('raw_post_id', $1::text, 'step', 'fetch', 'outcome', 'stopped', 'reason', 'fetch failed: status 404'))
		`, rawPostID, big); err != nil {
		t.Fatal(err)
	}

	rec := do(mux, http.MethodGet, "/admin/posts/"+rawPostID+"/trace", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	if rec.Body.Len() > 1<<16 {
		t.Errorf("trace is %d bytes, it carries a body", rec.Body.Len())
	}

	var got struct {
		Steps []struct {
			Reason string           `json:"reason"`
			Output map[string]any   `json:"output"`
			Calls  []map[string]any `json:"calls"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if len(got.Steps) != 1 || len(got.Steps[0].Calls) != 1 || got.Steps[0].Reason != "fetch failed: status 404" || got.Steps[0].Output["bytes"] != 0.0 {
		t.Fatalf("trace %s", rec.Body)
	}

	for _, key := range []string{"request", "response"} {
		if _, ok := got.Steps[0].Calls[0][key]; ok {
			t.Errorf("call has %s", key)
		}
	}

	if rec := do(mux, http.MethodGet, "/admin/posts/x/trace", ""); rec.Code != http.StatusNotFound {
		t.Errorf("bad id: %d", rec.Code)
	}
}

func TestResyncPost(t *testing.T) {
	mux, db, _, resyncs := newMuxResyncs(t)
	ctx := context.Background()

	ids := map[string]string{}

	for _, status := range []string{"rejected", "accepted", "pending"} {
		var id string
		if err := db.QueryRow(ctx, `
			WITH d AS (
				INSERT INTO domains (base_url) VALUES ('https://a.example')
				ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url
				RETURNING id
			)
			INSERT INTO raw_posts (url, domain_id, status) SELECT 'https://a.example/' || $1, id, $1 FROM d RETURNING id
			`, status).Scan(&id); err != nil {
			t.Fatal(err)
		}

		ids[status] = id
	}

	path := func(id string) string { return "/admin/posts/" + id + "/resync" }

	cases := []struct {
		name, id, body string
		want           int
	}{
		{"unknown", "00000000-0000-0000-0000-000000000000", `{}`, http.StatusNotFound},
		{"bad body", ids["rejected"], `{"override":"yes"}`, http.StatusBadRequest},
		{"override not rejected", ids["accepted"], `{"override":true}`, http.StatusBadRequest},
		{"in flight", ids["pending"], `{}`, http.StatusConflict},
		{"override", ids["rejected"], `{"override":true}`, http.StatusAccepted},
		{"queue full", ids["accepted"], `{}`, http.StatusServiceUnavailable},
	}

	for _, c := range cases {
		if rec := do(mux, http.MethodPost, path(c.id), c.body); rec.Code != c.want {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}

	if r := <-resyncs; r.RawPostID != ids["rejected"] || !r.Override {
		t.Errorf("queued %+v", r)
	}
}
