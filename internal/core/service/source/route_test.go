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

func TestQueueSource(t *testing.T) {
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

	for _, name := range []string{"sync", "backfill"} {
		if _, err := db.Exec(ctx, `UPDATE sources SET enabled = false WHERE id = $1`, sourceID); err != nil {
			t.Fatal(err)
		}

		queue := make(chan domain.Source, 1)
		handler := queueSource(&store, queue, name, slog.New(slog.DiscardHandler))

		send := func(id string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/sources/"+id+"/"+name, nil)
			req.SetPathValue("id", id)
			handler(rec, req)

			return rec
		}

		if rec := send(sourceID); rec.Code != http.StatusConflict {
			t.Errorf("%s disabled: %d", name, rec.Code)
		}

		if _, err := db.Exec(ctx, `UPDATE sources SET enabled = true WHERE id = $1`, sourceID); err != nil {
			t.Fatal(err)
		}

		if rec := send(sourceID); rec.Code != http.StatusAccepted || len(queue) != 1 {
			t.Errorf("%s active: %d, queued %d", name, rec.Code, len(queue))
		}

		if rec := send(sourceID); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), name+" queue full") {
			t.Errorf("%s queue full: %d %q", name, rec.Code, rec.Body.String())
		}

		for _, id := range []string{"00000000-0000-0000-0000-000000000000", "nope"} {
			if rec := send(id); rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d", name, id, rec.Code)
			}
		}
	}
}

func TestRoutesUnderAdmin(t *testing.T) {
	store := postgres.NewSourceStore(dbtest.New(t))
	mux := http.NewServeMux()
	Routes(mux, &store, make(chan domain.Source), make(chan domain.Source), slog.New(slog.DiscardHandler), slog.New(slog.DiscardHandler))

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/admin/listing", http.StatusBadRequest},
		{http.MethodPost, "/admin/sources/nope/sync", http.StatusNotFound},
		{http.MethodPost, "/admin/sources/nope/backfill", http.StatusNotFound},
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
