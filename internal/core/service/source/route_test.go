package source

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

func TestListingRoute(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed.xml" {
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Write([]byte(testFeedOf("Blogg")))
			return
		}

		if r.URL.Path == "/wp-json/wp/v2/posts" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-WP-Total", "420")
			w.Write([]byte(`[{"id":1}]`))
			return
		}

		if r.URL.Path == "/wp" {
			w.Header().Set("Link", `</wp-json/>; rel="https://api.w.org/"`)
		}

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`))
	}))
	defer site.Close()

	handler := listingRoute(site.Client(), slog.New(slog.DiscardHandler), slog.New(slog.DiscardHandler))

	cases := []struct{ path, want string }{
		{"/blog", `{"candidates":[{"adapter":"rss","url":"` + site.URL + `/feed.xml","title":"Blogg","items":2,"latest":"2026-09-16T00:00:00Z"},{"adapter":"page","url":"` + site.URL + `/blog"}]}`},
		{"/wp", `{"candidates":[{"adapter":"rss","url":"` + site.URL + `/feed.xml","title":"Blogg","items":2,"latest":"2026-09-16T00:00:00Z"},{"adapter":"page","url":"` + site.URL + `/wp"}],"wordpress":{"posts":"` + site.URL + `/wp-json/wp/v2/posts","total":420}}`},
		{"/feed.xml", `{"candidates":[{"adapter":"rss","url":"` + site.URL + `/feed.xml","title":"Blogg","items":2,"latest":"2026-09-16T00:00:00Z"}]}`},
	}

	for _, c := range cases {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, "/listing?url="+url.QueryEscape(site.URL+c.path), nil))

		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != c.want {
			t.Errorf("%s: %d %s, want %s", c.path, rec.Code, rec.Body, c.want)
		}
	}

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/listing?url="+url.QueryEscape("http://127.0.0.1:1/"), nil))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("unreachable: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/listing", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("no url: %d", rec.Code)
	}
}

func TestSyncSource(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	var sourceID string
	if err := db.QueryRow(ctx, `
		WITH d AS (INSERT INTO domains (base_url) VALUES ('https://a.example') RETURNING id)
		INSERT INTO sources (domain_id, adapter, url) SELECT id, 'rss', 'https://a.example/feed' FROM d RETURNING id
		`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}

	store := postgres.NewSourceStore(db)
	syncs := make(chan domain.Source, 1)
	handler := syncSource(&store, syncs, slog.New(slog.DiscardHandler))

	sync := func(id string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/sources/"+id+"/sync", nil)
		req.SetPathValue("id", id)
		handler(rec, req)

		return rec.Code
	}

	if code := sync(sourceID); code != http.StatusConflict {
		t.Errorf("disabled: %d", code)
	}

	if _, err := db.Exec(ctx, `UPDATE sources SET enabled = true WHERE id = $1`, sourceID); err != nil {
		t.Fatal(err)
	}

	if code := sync(sourceID); code != http.StatusAccepted || len(syncs) != 1 {
		t.Errorf("active: %d, queued %d", code, len(syncs))
	}

	if code := sync(sourceID); code != http.StatusServiceUnavailable {
		t.Errorf("queue full: %d", code)
	}

	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "nope"} {
		if code := sync(id); code != http.StatusNotFound {
			t.Errorf("%s: %d", id, code)
		}
	}
}

func TestRoutesUnderAdmin(t *testing.T) {
	store := postgres.NewSourceStore(dbtest.New(t))
	mux := http.NewServeMux()
	Routes(mux, &store, make(chan domain.Source), slog.New(slog.DiscardHandler), slog.New(slog.DiscardHandler))

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/admin/listing", http.StatusBadRequest},
		{http.MethodPost, "/admin/sources/nope/sync", http.StatusNotFound},
		{http.MethodGet, "/listing", http.StatusNotFound},
	}

	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))

		if rec.Code != c.want {
			t.Errorf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
}
