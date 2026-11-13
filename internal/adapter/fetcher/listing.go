package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/mmcdole/gofeed"
)

const (
	maxFeedLinks = 10
	probeTimeout = time.Minute
)

var _ port.Prober = Prober{}

// Prober tells the admin what a url can be added as.
type Prober struct {
	client *http.Client
	l      *slog.Logger
	calls  *slog.Logger
}

func NewProber(l, calls *slog.Logger) Prober {
	return Prober{client: safehttp.NewClient(20 * time.Second), l: l, calls: calls}
}

// Probing can take several requests, and the admin waits for all of them.
func (p Prober) Probe(ctx context.Context, rawURL string) (domain.Listing, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	c := logs.Client(p.client, p.calls.With("url", rawURL), "homepage")

	candidates, err := discover(ctx, c, rawURL)
	if err != nil {
		return domain.Listing{}, err
	}

	out := domain.Listing{Candidates: candidates}

	// Same rule as the backfill, so the admin is told only about an archive that gets read.
	if !siteWide(rawURL) {
		return out, nil
	}

	wp, err := FindWordPress(ctx, c, nil, rawURL)
	if err != nil {
		p.l.Warn("find wordpress", "url", rawURL, "err", err)
	}

	if wp.Posts != "" {
		out.Archive = &domain.Archive{URL: wp.Posts, Total: wp.Total}
	}

	return out, nil
}

// Tried in order when no linked feed works.
var feedPaths = []string{"/feed", "/rss", "/rss.xml", "/feed.xml", "/atom.xml", "/index.xml"}

// discover lists what a url can be read as, best first. A feed url is only itself. A page gives
// the feeds it links to, or failing those the feeds at common paths, and last the page itself for the linker.
// Every feed is fetched and parsed, so a broken one is left out.
func discover(ctx context.Context, client *http.Client, rawURL string) ([]domain.Candidate, error) {
	body, final, mt, err := FetchAny(ctx, client, rawURL)
	if err != nil {
		return nil, err
	}

	if !IsHTML(mt) {
		c, err := parseFeed(final, body)
		if err != nil {
			return nil, fmt.Errorf("err neither page nor feed: %s: %w", mt, err)
		}

		return []domain.Candidate{c}, nil
	}

	// Same rule as findListing: a redirect to another site keeps the source on its own url.
	if !sameSite(final, rawURL) {
		return []domain.Candidate{{Adapter: "page", URL: rawURL}}, nil
	}

	var out []domain.Candidate

	seen := make(map[string]bool)

	try := func(u string) {
		b, feedURL, _, err := FetchAny(ctx, client, u)
		if err != nil || seen[feedURL] {
			return
		}

		c, err := parseFeed(feedURL, b)
		if err != nil {
			return
		}

		seen[feedURL] = true
		out = append(out, c)
	}

	links := feedLinks(body, final)

	for _, link := range links[:min(len(links), maxFeedLinks)] {
		try(link)
	}

	if len(out) == 0 {
		base, err := url.Parse(final)
		if err != nil {
			return nil, fmt.Errorf("err parse final url: %w", err)
		}

		for _, p := range feedPaths {
			try(base.ResolveReference(&url.URL{Path: p}).String())
		}
	}

	return append(out, domain.Candidate{Adapter: "page", URL: final}), nil
}

func parseFeed(feedURL string, body []byte) (domain.Candidate, error) {
	f, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("err parse feed: %w", err)
	}

	c := domain.Candidate{Adapter: "rss", URL: feedURL, Title: strings.TrimSpace(f.Title), Items: len(f.Items)}

	for _, item := range f.Items {
		for _, t := range []*time.Time{item.PublishedParsed, item.UpdatedParsed} {
			if t != nil && t.After(c.Latest) {
				c.Latest = *t
			}
		}
	}

	return c, nil
}

var feedTypes = map[string]bool{
	"application/rss+xml":   true,
	"application/atom+xml":  true,
	"application/feed+json": true,
}

// The scheme is ignored, since an http source often lands on its https homepage.
func sameSite(pageURL, sourceURL string) bool {
	a, err := domain.BaseURL(pageURL)
	if err != nil {
		return false
	}

	b, err := domain.BaseURL(sourceURL)

	return err == nil && withoutScheme(a) == withoutScheme(b)
}

// feedLinks returns the feeds a page links to, in page order without repeats.
func feedLinks(page []byte, pageURL string) []string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return nil
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}

	var links []string

	doc.Find(`link[rel~="alternate"]`).Each(func(_ int, s *goquery.Selection) {
		typ := strings.ToLower(strings.TrimSpace(s.AttrOr("type", "")))
		href := strings.TrimSpace(s.AttrOr("href", ""))

		if !feedTypes[typ] || href == "" {
			return
		}

		ref, err := url.Parse(href)
		if err != nil {
			return
		}

		if link := base.ResolveReference(ref).String(); !slices.Contains(links, link) {
			links = append(links, link)
		}
	})

	return links
}

func withoutScheme(base string) string {
	_, host, _ := strings.Cut(base, "://")

	return host
}
