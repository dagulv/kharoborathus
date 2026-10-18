package source

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testFeed = `<?xml version="1.0"?><rss version="2.0"><channel><title>%s</title>
<item><title>A</title><link>https://a.example/a</link><pubDate>Mon, 02 Mar 2026 10:00:00 GMT</pubDate></item>
<item><title>B</title><link>https://a.example/b</link><pubDate>Wed, 16 Sep 2026 00:00:00 GMT</pubDate></item>
</channel></rss>`

func feedServer(t *testing.T, pages map[string]string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}

		if body[0] == '<' && body[1] == '?' {
			w.Header().Set("Content-Type", "application/rss+xml")
		} else {
			w.Header().Set("Content-Type", "text/html")
		}

		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func testFeedOf(title string) string {
	return fmt.Sprintf(testFeed, title)
}

func TestDiscoverLinkedFeeds(t *testing.T) {
	srv := feedServer(t, map[string]string{
		"/": `<html><head>
<link rel="alternate" type="application/rss+xml" href="/posts.xml">
<link rel="alternate" type="application/atom+xml" href="/broken">
<link rel="alternate" type="application/rss+xml" href="/posts.xml">
</head></html>`,
		"/posts.xml": testFeedOf("Posts"),
		"/feed.xml":  testFeedOf("Never probed"),
	})

	got, err := discover(context.Background(), srv.Client(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("candidates %+v", got)
	}

	if c := got[0]; c.Adapter != "rss" || c.URL != srv.URL+"/posts.xml" || c.Title != "Posts" || c.Items != 2 ||
		!c.Latest.Equal(time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("feed %+v", c)
	}

	if c := got[1]; c.Adapter != "page" || c.URL != srv.URL+"/" {
		t.Errorf("page %+v", c)
	}
}

func TestDiscoverProbesWithoutLinks(t *testing.T) {
	srv := feedServer(t, map[string]string{
		"/news":     `<html><body>Hej</body></html>`,
		"/rss.xml":  testFeedOf("Rss"),
		"/atom.xml": testFeedOf("Atom"),
	})

	got, err := discover(context.Background(), srv.Client(), srv.URL+"/news")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"rss " + srv.URL + "/rss.xml", "rss " + srv.URL + "/atom.xml", "page " + srv.URL + "/news"}
	if len(got) != len(want) {
		t.Fatalf("candidates %+v", got)
	}

	for i, c := range got {
		if c.Adapter+" "+c.URL != want[i] {
			t.Errorf("%d: %s %s, want %s", i, c.Adapter, c.URL, want[i])
		}
	}
}

func TestDiscoverFeedURL(t *testing.T) {
	srv := feedServer(t, map[string]string{"/feed.xml": testFeedOf("Direct")})

	got, err := discover(context.Background(), srv.Client(), srv.URL+"/feed.xml")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Adapter != "rss" || got[0].Title != "Direct" {
		t.Errorf("candidates %+v", got)
	}
}

func TestDiscoverOtherHost(t *testing.T) {
	other := feedServer(t, map[string]string{
		"/":         `<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`,
		"/feed.xml": testFeedOf("Other"),
	})

	srv := httptest.NewServer(http.RedirectHandler(other.URL+"/", http.StatusFound))
	defer srv.Close()

	got, err := discover(context.Background(), srv.Client(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Adapter != "page" || got[0].URL != srv.URL+"/" {
		t.Errorf("candidates %+v", got)
	}
}

func TestDiscoverNeitherPageNorFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("png"))
	}))
	defer srv.Close()

	if _, err := discover(context.Background(), srv.Client(), srv.URL+"/"); err == nil {
		t.Error("no error")
	}
}
