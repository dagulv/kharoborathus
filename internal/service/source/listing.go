package source

import (
	"bytes"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/service/domain"
)

func findListing(page []byte, pageURL, sourceURL string) (adapterName, listing string) {
	adapterName, listing = "page", sourceURL

	// The domain base drops www, but the page linker keeps only links on the listing's own host.
	if a, err := domain.BaseURL(pageURL); err == nil {
		if b, err := domain.BaseURL(sourceURL); err == nil && a == b {
			listing = pageURL
		}
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return
	}

	doc.Find(`link[rel~="alternate"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		typ := strings.ToLower(strings.TrimSpace(s.AttrOr("type", "")))
		href := strings.TrimSpace(s.AttrOr("href", ""))

		if (typ != "application/rss+xml" && typ != "application/atom+xml") || href == "" {
			return true
		}

		ref, err := url.Parse(href)
		if err != nil {
			return true
		}

		adapterName, listing = "rss", base.ResolveReference(ref).String()

		return false
	})

	return
}
