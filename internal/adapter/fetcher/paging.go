package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
)

var _ port.Backfiller[pageDocument] = page{}

// pager gives the url of listing page n, the page after cur. "" ends the run.
type pager func(ctx context.Context, l *slog.Logger, d domain.Domain, cur string, body []byte, n int) (string, error)

// Backfill reads a listing's older pages with plain requests, no browser. Each page's article links
// come from the domain's links rule, so a domain without one has no archive yet.
func (r page) Backfill(ctx context.Context, l *slog.Logger, rawURL string) (<-chan pageDocument, error) {
	wp, ok, err := wordpressArchive(ctx, logs.Client(r.client, l, "wordpress"), r.pacer, l, rawURL)
	if err != nil {
		return nil, err
	}

	if ok {
		return wordpressLinks(ctx, wp), nil
	}

	client := logs.Client(r.client, l, "page")

	if err := r.pacer.Wait(ctx, rawURL); err != nil {
		return nil, err
	}

	first, cur, err := FetchPage(ctx, client, rawURL)
	if err != nil {
		return nil, fmt.Errorf("err fetch listing: %w", err)
	}

	d, err := r.domains.GetOrCreateDomain(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("err get domain: %w", err)
	}

	links, err := r.linker.Links(ctx, l, d, cur, first)
	if err != nil {
		return nil, fmt.Errorf("err links: %w", err)
	}

	if len(links) == 0 {
		return nil, fmt.Errorf("%w: no links on %s", port.ErrNoArchive, rawURL)
	}

	next, more, err := r.pick(ctx, l, d, client, cur, first, links)
	if err != nil {
		return nil, err
	}

	if more == nil {
		return nil, fmt.Errorf("%w: no next page on %s", port.ErrNoArchive, rawURL)
	}

	lang := htmlLang(first)
	out := make(chan pageDocument)

	go func() {
		defer close(out)

		seen := make(map[string]bool)
		visited := map[string]bool{cur: true}
		body := first

		send := func(links []string) (sent int, ok bool) {
			for _, link := range links {
				if seen[link] {
					continue
				}

				seen[link] = true
				sent++

				select {
				case out <- pageDocument{URL: link, Lang: lang}:
				case <-ctx.Done():
					return sent, false
				}
			}

			return sent, true
		}

		if _, ok := send(links); !ok {
			return
		}

		for n := 2; next != "" && !visited[next]; n++ {
			visited[next] = true
			cur = next

			if err := r.pacer.Wait(ctx, cur); err != nil {
				return
			}

			if body, _, err = FetchPage(ctx, client, cur); err != nil {
				l.Info("backfill stopped", "url", cur, "err", err)
				return
			}

			links, err := r.linker.Links(ctx, l, d, cur, body)
			if err != nil {
				l.Error("links", "url", cur, "err", err)
				return
			}

			if sent, ok := send(links); !ok || sent == 0 {
				return
			}

			if next, err = more(ctx, l, d, cur, body, n+1); err != nil {
				l.Error("next page", "url", cur, "err", err)
				return
			}
		}
	}()

	return out, nil
}

// pick tries rel next, url patterns and the pagination rule in order, and keeps the first that finds page 2.
func (r page) pick(ctx context.Context, l *slog.Logger, d domain.Domain, client *http.Client, cur string, body []byte, links []string) (string, pager, error) {
	for _, p := range []pager{relNext, r.patterns(client, cur, links), r.ruled} {
		next, err := p(ctx, l, d, cur, body, 2)
		if err != nil {
			return "", nil, err
		}

		if next != "" {
			return next, p, nil
		}
	}

	return "", nil, nil
}

func relNext(_ context.Context, _ *slog.Logger, _ domain.Domain, cur string, body []byte, _ int) (string, error) {
	base, err := url.Parse(cur)
	if err != nil {
		return "", fmt.Errorf("err parse url: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("err parse listing: %w", err)
	}

	for _, sel := range []string{`link[rel~="next"]`, `a[rel~="next"]`} {
		href := strings.TrimSpace(doc.Find(sel).First().AttrOr("href", ""))
		if href == "" {
			continue
		}

		if u, err := base.Parse(href); err == nil && u.Host == base.Host {
			return u.String(), nil
		}
	}

	return "", nil
}

var pagePatterns = []func(base url.URL, n int) url.URL{
	func(u url.URL, n int) url.URL {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/page/" + strconv.Itoa(n) + "/"
		return u
	},
	queryPattern("page"),
	queryPattern("paged"),
	queryPattern("p"),
}

func queryPattern(key string) func(url.URL, int) url.URL {
	return func(u url.URL, n int) url.URL {
		q := u.Query()
		q.Set(key, strconv.Itoa(n))
		u.RawQuery = q.Encode()

		return u
	}
}

// patterns keeps the first url pattern whose page 2 shows a link page 1 did not, and counts up with it.
func (r page) patterns(client *http.Client, first string, links []string) pager {
	var chosen func(url.URL, int) url.URL

	known := make(map[string]bool, len(links))
	for _, link := range links {
		known[link] = true
	}

	return func(ctx context.Context, l *slog.Logger, d domain.Domain, _ string, _ []byte, n int) (string, error) {
		base, err := url.Parse(first)
		if err != nil {
			return "", fmt.Errorf("err parse url: %w", err)
		}

		if chosen != nil {
			u := chosen(*base, n)
			return u.String(), nil
		}

		for _, pattern := range pagePatterns {
			u := pattern(*base, n)
			candidate := u.String()

			if err := r.pacer.Wait(ctx, candidate); err != nil {
				return "", err
			}

			body, _, err := FetchPage(ctx, client, candidate)
			if err != nil {
				continue
			}

			links, err := r.linker.Links(ctx, l, d, candidate, body)
			if err != nil {
				continue
			}

			for _, link := range links {
				if !known[link] {
					chosen = pattern
					return candidate, nil
				}
			}
		}

		return "", nil
	}
}

func (r page) ruled(ctx context.Context, l *slog.Logger, d domain.Domain, cur string, body []byte, n int) (string, error) {
	return r.linker.Next(ctx, l, d, cur, body, n)
}

// A WordPress page source keeps only each post's link and language, and fetches the post like any listing link.
func wordpressLinks(ctx context.Context, docs <-chan rssDocument) <-chan pageDocument {
	out := make(chan pageDocument)

	go func() {
		defer close(out)

		for doc := range docs {
			select {
			case out <- pageDocument{URL: doc.URL, Lang: doc.Lang}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func htmlLang(page []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return ""
	}

	return doc.Find("html").AttrOr("lang", "")
}
