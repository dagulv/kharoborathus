package source

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dagulv/kharoborathus/internal/service/source/adapter"
	"github.com/mmcdole/gofeed"
)

const maxFeedLinks = 10

// Tried in order when no linked feed works.
var feedPaths = []string{"/feed", "/rss", "/rss.xml", "/feed.xml", "/atom.xml", "/index.xml"}

type Candidate struct {
	Adapter string    `json:"adapter"`
	URL     string    `json:"url"`
	Title   string    `json:"title,omitzero"`
	Items   int       `json:"items,omitzero"`
	Latest  time.Time `json:"latest,omitzero"`
}

// discover lists what a url can be read as, best first. A feed url is only itself. A page gives
// the feeds it links to, or failing those the feeds at common paths, and last the page itself for the linker.
// Every feed is fetched and parsed, so a broken one is left out.
func discover(ctx context.Context, client *http.Client, rawURL string) ([]Candidate, error) {
	body, final, mt, err := adapter.FetchAny(ctx, client, rawURL)
	if err != nil {
		return nil, err
	}

	if !adapter.IsHTML(mt) {
		c, err := parseFeed(final, body)
		if err != nil {
			return nil, fmt.Errorf("err neither page nor feed: %s: %w", mt, err)
		}

		return []Candidate{c}, nil
	}

	// Same rule as findListing: a redirect to another site keeps the source on its own url.
	if !sameSite(final, rawURL) {
		return []Candidate{{Adapter: "page", URL: rawURL}}, nil
	}

	var out []Candidate

	seen := make(map[string]bool)

	try := func(u string) {
		b, feedURL, _, err := adapter.FetchAny(ctx, client, u)
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

	return append(out, Candidate{Adapter: "page", URL: final}), nil
}

func parseFeed(feedURL string, body []byte) (Candidate, error) {
	f, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return Candidate{}, fmt.Errorf("err parse feed: %w", err)
	}

	c := Candidate{Adapter: "rss", URL: feedURL, Title: strings.TrimSpace(f.Title), Items: len(f.Items)}

	for _, item := range f.Items {
		for _, t := range []*time.Time{item.PublishedParsed, item.UpdatedParsed} {
			if t != nil && t.After(c.Latest) {
				c.Latest = *t
			}
		}
	}

	return c, nil
}
