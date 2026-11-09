package source

import (
	"bytes"
	"net/url"
	"slices"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/core/domain"
)

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
