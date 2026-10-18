package source

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestListingRoute(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed.xml" {
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Write([]byte(testFeedOf("Blogg")))
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`))
	}))
	defer site.Close()

	handler := listingRoute(site.Client(), slog.New(slog.DiscardHandler), slog.New(slog.DiscardHandler))

	cases := []struct{ path, want string }{
		{"/blog", `{"candidates":[{"adapter":"rss","url":"` + site.URL + `/feed.xml","title":"Blogg","items":2,"latest":"2026-09-16T00:00:00Z"},{"adapter":"page","url":"` + site.URL + `/blog"}]}`},
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
