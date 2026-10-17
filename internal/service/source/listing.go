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

	// A homepage redirect to another host must not move the source there, so its feed is ignored too.
	a, err := domain.BaseURL(pageURL)
	if err != nil {
		return
	}

	// The scheme is ignored, since an http source often lands on its https homepage.
	if b, err := domain.BaseURL(sourceURL); err != nil || withoutScheme(a) != withoutScheme(b) {
		return
	}

	// The domain base drops www, but the page linker keeps only links on the listing's own host.
	listing = pageURL

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

func withoutScheme(base string) string {
	_, host, _ := strings.Cut(base, "://")

	return host
}
