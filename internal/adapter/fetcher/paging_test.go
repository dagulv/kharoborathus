package fetcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
)

type fakeDomains struct{}

func (fakeDomains) GetOrCreateDomain(context.Context, string) (domain.Domain, error) {
	return domain.Domain{ID: "d"}, nil
}

// Links are the a.post elements. Next comes from a fixed map, standing in for a pagination rule.
type fakeLinker struct {
	next map[string]string
}

func (f fakeLinker) Links(_ context.Context, _ *slog.Logger, _ domain.Domain, rawURL string, listing []byte) ([]string, error) {
	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(listing))
	if err != nil {
		return nil, err
	}

	var links []string

	doc.Find("a.post").Each(func(_ int, s *goquery.Selection) {
		if u, err := base.Parse(s.AttrOr("href", "")); err == nil {
			links = append(links, u.String())
		}
	})

	return links, nil
}

func (f fakeLinker) Next(_ context.Context, _ *slog.Logger, _ domain.Domain, url string, _ []byte, _ int) (string, error) {
	return f.next[url], nil
}

func listingHTML(head string, posts ...string) string {
	var b strings.Builder

	for _, p := range posts {
		fmt.Fprintf(&b, `<a class="post" href="/%s">%s</a>`, p, p)
	}

	return fmt.Sprintf(`<html lang="sv"><head>%s</head><body>%s</body></html>`, head, b.String())
}

// Serves pages by request uri. Anything else is a 404.
func listingServer(t *testing.T, pages map[string]string, requests *atomic.Int32) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			requests.Add(1)
		}

		page, ok := pages[r.URL.RequestURI()]
		if !ok {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func backfillPage(t *testing.T, srv *httptest.Server, linker Linker, path string) ([]string, error) {
	t.Helper()

	l := slog.New(slog.DiscardHandler)
	r := page{client: srv.Client(), pacer: pace.New(time.Millisecond), domains: fakeDomains{}, linker: linker, l: l}

	docs, err := r.Backfill(context.Background(), l, srv.URL+path)
	if err != nil {
		return nil, err
	}

	var got []string
	for doc := range docs {
		got = append(got, strings.TrimPrefix(doc.URL, srv.URL+"/"))
	}

	return got, nil
}

func TestPageBackfillRelNext(t *testing.T) {
	srv := listingServer(t, map[string]string{
		"/news":        listingHTML(`<link rel="next" href="/news?page=2">`, "p1", "p2"),
		"/news?page=2": listingHTML(`<link rel="next" href="/news?page=3">`, "p3"),
		"/news?page=3": listingHTML("", "p4"),
	}, nil)

	got, err := backfillPage(t, srv, fakeLinker{}, "/news")
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"p1", "p2", "p3", "p4"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestPageBackfillPattern(t *testing.T) {
	srv := listingServer(t, map[string]string{
		"/news":        listingHTML("", "p1", "p2"),
		"/news?page=2": listingHTML("", "p3"),
		"/news?page=3": listingHTML("", "p3"),
	}, nil)

	got, err := backfillPage(t, srv, fakeLinker{}, "/news")
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"p1", "p2", "p3"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestPageBackfillRepeatsPageOne(t *testing.T) {
	first := listingHTML("", "p1", "p2")

	var requests atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(first))
	}))
	t.Cleanup(srv.Close)

	if _, err := backfillPage(t, srv, fakeLinker{}, "/news"); !errors.Is(err, port.ErrNoArchive) {
		t.Errorf("err %v, want ErrNoArchive", err)
	}

	if n := requests.Load(); n > 6 {
		t.Errorf("%d requests", n)
	}
}

func TestPageBackfillRule(t *testing.T) {
	srv := listingServer(t, map[string]string{
		"/news":      listingHTML("", "p1"),
		"/older?c=x": listingHTML("", "p5"),
	}, nil)

	got, err := backfillPage(t, srv, fakeLinker{next: map[string]string{srv.URL + "/news": srv.URL + "/older?c=x"}}, "/news")
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"p1", "p5"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

type noLinks struct{ fakeLinker }

func (noLinks) Links(context.Context, *slog.Logger, domain.Domain, string, []byte) ([]string, error) {
	return nil, nil
}

func TestPageBackfillNoLinkRule(t *testing.T) {
	srv := listingServer(t, map[string]string{
		"/news": listingHTML(`<link rel="next" href="/news?page=2">`, "p1"),
	}, nil)

	if _, err := backfillPage(t, srv, noLinks{}, "/news"); !errors.Is(err, port.ErrNoArchive) {
		t.Errorf("err %v, want ErrNoArchive", err)
	}
}

func TestPageBackfillWordPress(t *testing.T) {
	srv := wordpressServer(t, "/wp-json/")
	l := slog.New(slog.DiscardHandler)
	r := page{client: srv.Client(), pacer: pace.New(time.Millisecond), domains: fakeDomains{}, linker: fakeLinker{}, l: l}

	// A section like /blog/ may be one category of the site, so only the site root reads the whole API.
	if _, err := r.Backfill(context.Background(), l, srv.URL+"/blog/"); !errors.Is(err, port.ErrNoArchive) {
		t.Errorf("section: err %v, want ErrNoArchive", err)
	}

	docs, err := r.Backfill(context.Background(), l, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for doc := range docs {
		if doc.Lang != "sv" {
			t.Errorf("%s: lang %q", doc.URL, doc.Lang)
		}

		got = append(got, doc.URL)
	}

	if want := []string{"https://x.example/a", "https://x.example/b", "https://x.example/c"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}
