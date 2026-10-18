package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

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

func TestWordPressFetchesEveryPage(t *testing.T) {
	srv := wordpressServer(t, "/wp-json/")
	l := slog.New(slog.DiscardHandler)

	gap := 50 * time.Millisecond
	wp := wordpress{rss{client: srv.Client(), pacer: pace.New(gap), l: l}}

	start := time.Now()

	docs, err := wp.FetchFromSource(context.Background(), l, srv.URL+"/wp-json/wp/v2/posts")
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

	f, err := wp.ProcessFeed(context.Background(), a)
	if err != nil || !f.SummaryPending {
		t.Errorf("summary pending %v, %v", f.SummaryPending, err)
	}
}
