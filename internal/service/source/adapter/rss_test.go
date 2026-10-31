package adapter

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
</channel></rss>`))
	})

	mux.HandleFunc("/excerpt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><article>whole article</article></body></html>`))
	})

	mux.HandleFunc("/full", func(w http.ResponseWriter, r *http.Request) {
		t.Error("fetched an article the feed carried in full")
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func TestRSSFetchesArticleOfExcerptItem(t *testing.T) {
	srv := rssServer(t)
	l := slog.New(slog.DiscardHandler)
	r := rss{client: srv.Client(), pacer: pace.New(time.Millisecond), l: l}

	docs, err := r.FetchFromSource(context.Background(), l, srv.URL+"/feed")
	if err != nil {
		t.Fatal(err)
	}

	pages := make(map[string]string)

	for doc := range docs {
		doc, err := r.FetchContent(context.Background(), l, doc)
		if err != nil {
			t.Fatalf("fetch content %s: %v", doc.Title, err)
		}

		f, err := r.ProcessFeed(context.Background(), doc)
		if err != nil {
			t.Fatal(err)
		}

		pages[f.Title] = string(f.Page)
	}

	if want := `<html><body><article>whole article</article></body></html>`; pages["Excerpt"] != want {
		t.Errorf("excerpt item page %q, want %q", pages["Excerpt"], want)
	}

	if pages["Full"] != "" {
		t.Errorf("full item page %q, want none", pages["Full"])
	}
}
