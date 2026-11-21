package fetcher

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/pace"
)

func rssServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>T</title>
<item><title>Excerpt</title><link>` + srv.URL + `/excerpt</link><description>Short… Continue reading</description></item>
<item><title>Full</title><link>` + srv.URL + `/full</link><description>Short</description><content:encoded><![CDATA[<p>whole article</p>]]></content:encoded></item>
<item><title>Gone</title><link>` + srv.URL + `/gone</link><content:encoded><![CDATA[<p>feed only</p>]]></content:encoded></item>
</channel></rss>`))
	})

	mux.HandleFunc("/excerpt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><article>whole article</article></body></html>`))
	})

	mux.HandleFunc("/full", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><article>full page</article></body></html>`))
	})

	mux.HandleFunc("/gone", http.NotFound)

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func TestRSSSendsFeedItems(t *testing.T) {
	srv := rssServer(t)

	var requests atomic.Int32
	counted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(counted.Close)

	l := slog.New(slog.DiscardHandler)
	r := rss{client: counted.Client(), pacer: pace.New(time.Millisecond)}

	posts, err := r.Fetch(context.Background(), l, counted.URL+"/feed")
	if err != nil {
		t.Fatal(err)
	}

	contents := make(map[string]string)

	for p := range posts {
		if p.RawContent != "" {
			t.Errorf("%s: raw content %q, the pipeline fetches articles", p.Title, p.RawContent)
		}

		contents[p.Title] = p.Content
	}

	if len(contents) != 3 || contents["Full"] != "<p>whole article</p>" || contents["Gone"] != "<p>feed only</p>" || contents["Excerpt"] != "Short… Continue reading" {
		t.Errorf("contents %v", contents)
	}

	if n := requests.Load(); n != 1 {
		t.Errorf("%d requests, want only the feed", n)
	}
}
