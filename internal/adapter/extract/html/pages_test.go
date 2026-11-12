package html

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

const pagerListing = `<html><head><link rel="next" href="/news?page=2"></head><body>
<article class="teaser"><h2><a href="/artikel/1">A</a></h2></article>
<nav class="pager"><a class="older" rel="next" href="/news?page=2">Äldre nyheter</a></nav>
<button class="more" data-url="/news?page=3">Load more</button>
<a href="https://other.example/x">Off</a>
<a href="/news">Self</a>
</body></html>`

var teaserLinks = LinkRule{Links: str("article.teaser h2 a"), Reasoning: "teasers"}

// Eight teasers from first, and an older link to next.
func pagedListing(first int, next string) string {
	var b strings.Builder

	for n := first; n < first+8; n++ {
		fmt.Fprintf(&b, `<article class="teaser"><h2><a href="/artikel/%d">Rubrik %d</a></h2></article>`, n, n)
	}

	return fmt.Sprintf(`<html><body><main>%s</main><nav class="pager"><a class="older" href="%s">Äldre</a></nav></body></html>`, b.String(), next)
}

func TestPageCandidates(t *testing.T) {
	got := pageCandidates(sample{URL: "https://a.example/news", Data: []byte(pagerListing)}, teaserLinks)

	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}

	if c := got[0]; c.tag != "a" || c.rel != "next" || c.text != "Äldre nyheter" || c.url != "https://a.example/news?page=2" {
		t.Errorf("first %+v", c)
	}

	if c := got[1]; c.tag != "button" || c.text != "Load more" || c.url != "https://a.example/news?page=3" {
		t.Errorf("second %+v", c)
	}
}

func TestApplyPage(t *testing.T) {
	s := sample{URL: "https://a.example/news", Data: []byte(pagerListing)}

	cases := []struct {
		name string
		rule PageRule
		n    int
		want string
	}{
		{"next href", PageRule{Next: str("a.older")}, 2, "https://a.example/news?page=2"},
		{"next data-url", PageRule{Next: str("button.more")}, 2, "https://a.example/news?page=3"},
		{"next no match", PageRule{Next: str("a.missing")}, 2, ""},
		{"template", PageRule{Template: str("https://a.example/news?page={n}")}, 3, "https://a.example/news?page=3"},
		{"template off host", PageRule{Template: str("https://other.example/news?page={n}")}, 3, ""},
	}

	for _, c := range cases {
		got, err := applyPage(c.rule, s, c.n)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}

		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func pagerServer(t *testing.T, pages map[string]string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func fetchWith(srv *httptest.Server) fetchFunc {
	return func(ctx context.Context, url string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		res, err := srv.Client().Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()

		return io.ReadAll(res.Body)
	}
}

func TestCheckPageRule(t *testing.T) {
	next := PageRule{Next: str("a.older"), Reasoning: "older link"}

	t.Run("new links", func(t *testing.T) {
		srv := pagerServer(t, map[string]string{
			"/news?page=2": pagedListing(8, "/news?page=3"),
		})
		s := sample{URL: srv.URL + "/news", Data: []byte(pagedListing(0, "/news?page=2"))}

		if err := checkPageRule(context.Background(), next, teaserLinks, s, fetchWith(srv)); err != nil {
			t.Error(err)
		}
	})

	t.Run("repeated links", func(t *testing.T) {
		srv := pagerServer(t, map[string]string{
			"/news?page=2": pagedListing(0, "/news?page=3"),
		})
		s := sample{URL: srv.URL + "/news", Data: []byte(pagedListing(0, "/news?page=2"))}

		if err := checkPageRule(context.Background(), next, teaserLinks, s, fetchWith(srv)); err == nil {
			t.Error("page 2 repeating page 1 passed")
		}
	})

	t.Run("no progress", func(t *testing.T) {
		srv := pagerServer(t, map[string]string{
			"/news?page=2": pagedListing(8, "/news?page=2"),
		})
		s := sample{URL: srv.URL + "/news", Data: []byte(pagedListing(0, "/news?page=2"))}

		if err := checkPageRule(context.Background(), next, teaserLinks, s, fetchWith(srv)); err == nil {
			t.Error("page 3 equal to page 2 passed")
		}
	})
}

func linkRuled(t *testing.T, extra map[string]any) domain.Domain {
	t.Helper()

	rules := map[string]json.RawMessage{}

	for key, rule := range map[string]any{"links": teaserLinks} {
		raw, err := json.Marshal(rule)
		if err != nil {
			t.Fatal(err)
		}

		rules[key] = raw
	}

	for key, rule := range extra {
		raw, err := json.Marshal(rule)
		if err != nil {
			t.Fatal(err)
		}

		rules[key] = raw
	}

	return domain.Domain{ID: "d", BaseURL: "https://a.example", Metadata: domain.Metadata{Extract: rules}}
}

func TestNextGeneratesWithoutRule(t *testing.T) {
	srv := pagerServer(t, map[string]string{
		"/news?page=2": pagedListing(8, "/news?page=3"),
	})

	llm := &fakeLLM{answers: []any{PageRule{Next: str("a.older"), Reasoning: "older link"}}}
	store := &fakeStore{}
	tr := newExtractor(llm, store, nil, nil, discard)
	tr.client = srv.Client()

	got, err := tr.Next(context.Background(), discard, linkRuled(t, nil), srv.URL+"/news", []byte(pagedListing(0, "/news?page=2")), 2)
	if err != nil || got != "" {
		t.Errorf("got %q, %v", got, err)
	}

	wait(tr)

	store.mu.Lock()
	defer store.mu.Unlock()

	if _, ok := store.rules[string(fieldPages)]; !ok {
		t.Errorf("rules %v", store.rules)
	}
}

func TestNextWithoutLinkRule(t *testing.T) {
	llm := &fakeLLM{answers: []any{PageRule{Next: str("a.older")}}}
	tr := newExtractor(llm, &fakeStore{}, nil, nil, discard)

	got, err := tr.Next(context.Background(), discard, domain.Domain{ID: "d"}, "https://a.example/news", []byte(pagerListing), 2)
	if err != nil || got != "" {
		t.Errorf("got %q, %v", got, err)
	}

	wait(tr)

	if llm.calls != 0 {
		t.Errorf("%d llm calls", llm.calls)
	}
}

func TestNextAppliesRule(t *testing.T) {
	tr := newExtractor(&fakeLLM{}, &fakeStore{}, nil, nil, discard)
	d := linkRuled(t, map[string]any{"pages": PageRule{Template: str("https://a.example/news?page={n}")}})

	got, err := tr.Next(context.Background(), discard, d, "https://a.example/news?page=4", []byte(pagerListing), 5)
	if err != nil || got != "https://a.example/news?page=5" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestNextTemplateOnlyOnItsListing(t *testing.T) {
	tr := newExtractor(&fakeLLM{}, &fakeStore{}, nil, nil, discard)
	d := linkRuled(t, map[string]any{"pages": storedPageRule{
		PageRule: PageRule{Template: str("https://a.example/news/archive/{n}")},
		Listing:  "https://a.example/news/",
	}})

	cases := []struct {
		cur  string
		n    int
		want string
	}{
		{"https://a.example/news/", 2, "https://a.example/news/archive/2"},
		{"https://a.example/news/archive/2", 3, "https://a.example/news/archive/3"},
		{"https://a.example/press/", 2, ""},
		{"https://a.example/press/archive/2", 3, ""},
	}

	for _, c := range cases {
		got, err := tr.Next(context.Background(), discard, d, c.cur, []byte(pagerListing), c.n)
		if err != nil || got != c.want {
			t.Errorf("%s page %d: got %q, %v, want %q", c.cur, c.n, got, err, c.want)
		}
	}
}
