package fetcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
)

func wordpressServer(t *testing.T, root string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="%s", <https://x.example/>; rel="canonical"`, root, wordpressRel))
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>T</title><language>sv-SE</language></channel></rss>`))
	})

	mux.HandleFunc("/category/x/feed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="%s"`, root, wordpressRel))
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>T</title></channel></rss>`))
	})

	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html lang="sv"><head><link rel="%s" href="%s"></head></html>`, wordpressRel, root)
	})

	mux.HandleFunc("/blog/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html lang="sv"><head><link rel="%s" href="%s"></head></html>`, wordpressRel, root)
	})

	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html lang="en"></html>`))
	})

	posts := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-WP-Total", "3")
		w.Header().Set("X-WP-TotalPages", "2")

		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(`[{"link":"https://x.example/c","date_gmt":"2014-10-17T17:23:58","title":{"rendered":"C"},"content":{"rendered":"<p>c</p>"}}]`))
			return
		}

		w.Write([]byte(`[
{"link":"https://x.example/a","date_gmt":"2026-09-18T15:48:24","title":{"rendered":"A &#8211; b"},"content":{"rendered":"<p>a</p>"},
 "_embedded":{"author":[{"name":"Olle"}],"wp:term":[[{"name":"Aktier &amp; ekonomi"}],[{"name":"fonder"}]]}},
{"link":"https://x.example/b","date_gmt":"2026-07-01T00:00:00","title":{"rendered":"B"},"content":{"rendered":"<p>b</p>"}}
]`))
	}

	mux.HandleFunc("/wp-json/wp/v2/posts", posts)
	mux.HandleFunc("/index.php", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("rest_route") != "/wp/v2/posts" {
			http.NotFound(w, r)
			return
		}

		posts(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func TestFindWordPress(t *testing.T) {
	cases := []struct {
		name, path, root, posts, lang string
	}{
		{"feed link header", "/feed", "/wp-json/", "/wp-json/wp/v2/posts", "sv-SE"},
		{"page link tag", "/blog/", "/wp-json/", "/wp-json/wp/v2/posts", "sv"},
		{"rest route root", "/blog/", "/index.php?rest_route=/", "/index.php?rest_route=%2Fwp%2Fv2%2Fposts", "sv"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := wordpressServer(t, c.root)

			wp, err := FindWordPress(context.Background(), srv.Client(), nil, srv.URL+c.path)
			if err != nil {
				t.Fatal(err)
			}

			want := WordPress{Posts: srv.URL + c.posts, Total: 3, Lang: c.lang}
			if wp != want {
				t.Errorf("got %+v, want %+v", wp, want)
			}
		})
	}
}

func TestFindWordPressNone(t *testing.T) {
	srv := wordpressServer(t, "/wp-json/")

	wp, err := FindWordPress(context.Background(), srv.Client(), nil, srv.URL+"/plain")
	if err != nil || wp != (WordPress{}) {
		t.Errorf("got %+v, %v", wp, err)
	}
}

func TestRSSBackfillWordPress(t *testing.T) {
	srv := wordpressServer(t, "/wp-json/")
	l := slog.New(slog.DiscardHandler)

	gap := 50 * time.Millisecond
	r := rss{client: srv.Client(), pacer: pace.New(gap), l: l}
	r.archives = []archive{r.wordpress}

	start := time.Now()

	docs, err := r.Backfill(context.Background(), l, srv.URL+"/feed")
	if err != nil {
		t.Fatal(err)
	}

	var got []rssDocument
	for doc := range docs {
		got = append(got, doc)
	}

	if len(got) != 3 || got[2].URL != "https://x.example/c" {
		t.Fatalf("got %+v", got)
	}

	for _, doc := range got {
		if doc.Lang != "sv-SE" {
			t.Errorf("%s: lang %q", doc.URL, doc.Lang)
		}
	}

	if elapsed := time.Since(start); elapsed < gap {
		t.Errorf("two pages took %v, want at least %v apart", elapsed, gap)
	}

	a := got[0]

	if a.Title != "A – b" || a.Content != "<p>a</p>" || !a.PostedAt.Equal(time.Date(2026, 9, 18, 15, 48, 24, 0, time.UTC)) {
		t.Errorf("got %+v", a)
	}

	if !slices.Equal(a.Authors, []string{"Olle"}) || !slices.Equal(a.Tags, []string{"Aktier & ekonomi", "fonder"}) {
		t.Errorf("authors %v tags %v", a.Authors, a.Tags)
	}
}

func TestRSSBackfillOrder(t *testing.T) {
	l := slog.New(slog.DiscardHandler)

	found := make(chan rssDocument, 1)
	found <- rssDocument{URL: "x"}
	close(found)

	r := rss{l: l, archives: []archive{
		func(context.Context, *slog.Logger, string) (<-chan rssDocument, bool, error) { return nil, false, nil },
		func(context.Context, *slog.Logger, string) (<-chan rssDocument, bool, error) { return found, true, nil },
		func(context.Context, *slog.Logger, string) (<-chan rssDocument, bool, error) {
			t.Error("tried a strategy after one was found")
			return nil, false, nil
		},
	}}

	docs, err := r.Backfill(context.Background(), l, "https://a.example/feed")
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for doc := range docs {
		got = append(got, doc.URL)
	}

	if !slices.Equal(got, []string{"x"}) {
		t.Errorf("got %v", got)
	}
}

func TestRSSBackfillNone(t *testing.T) {
	srv := wordpressServer(t, "/wp-json/")
	l := slog.New(slog.DiscardHandler)

	r := rss{client: srv.Client(), pacer: pace.New(time.Millisecond), l: l}
	r.archives = []archive{r.wordpress}

	if _, err := r.Backfill(context.Background(), l, srv.URL+"/plain"); !errors.Is(err, port.ErrNoArchive) {
		t.Errorf("err %v, want ErrNoArchive", err)
	}
}

func TestRSSBackfillCategoryFeed(t *testing.T) {
	srv := wordpressServer(t, "/wp-json/")
	l := slog.New(slog.DiscardHandler)

	r := rss{client: srv.Client(), pacer: pace.New(time.Millisecond), l: l}
	r.archives = []archive{r.wordpress}

	// The API lists every post on the site, not only the category's.
	if _, err := r.Backfill(context.Background(), l, srv.URL+"/category/x/feed"); !errors.Is(err, port.ErrNoArchive) {
		t.Errorf("err %v, want ErrNoArchive", err)
	}
}
