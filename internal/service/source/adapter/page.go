package adapter

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/service/feed"
)

var _ Adapter[pageDocument] = page{}

type pageDocument struct {
	URL  string
	Lang string
	Page []byte
}

type page struct {
	client    *http.Client
	feedStore *feed.Store
	l         *slog.Logger
}

func NewPage(feedStore *feed.Store, l *slog.Logger) Adapter[pageDocument] {
	return page{
		client: &http.Client{
			Timeout: time.Second * 20,
		},
		feedStore: feedStore,
		l:         l,
	}
}

func (r page) FetchFromSource(ctx context.Context, rawURL string) (<-chan pageDocument, error) {
	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("err parse page url: %w", err)
	}

	listing, err := FetchHTML(ctx, r.client, rawURL)
	if err != nil {
		return nil, fmt.Errorf("err fetch listing: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(listing))
	if err != nil {
		return nil, fmt.Errorf("err parse listing: %w", err)
	}

	lang, _ := doc.Find("html").Attr("lang")
	out := make(chan pageDocument)

	go func() {
		defer close(out)

		seen := map[string]bool{base.String(): true}

		for _, s := range doc.Find("a[href]").EachIter() {
			href, _ := s.Attr("href")

			u, err := base.Parse(strings.TrimSpace(href))
			if err != nil || u.Host != base.Host || (u.Scheme != "http" && u.Scheme != "https") {
				continue
			}

			u.Fragment = ""
			link := u.String()

			if seen[link] {
				continue
			}

			seen[link] = true

			select {
			case out <- pageDocument{URL: link, Lang: lang}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

func (r page) Filter(ctx context.Context, data pageDocument) (bool, error) {
	exists, err := r.feedStore.FeedExists(ctx, data.URL)
	if err != nil {
		return false, fmt.Errorf("err feed exists: %w", err)
	}

	return !exists, nil
}

func (r page) FetchContent(ctx context.Context, data pageDocument) (pageDocument, error) {
	page, err := FetchHTML(ctx, r.client, data.URL)
	if err != nil {
		return data, fmt.Errorf("err fetch article: %w", err)
	}

	data.Page = page

	return data, nil
}

func (r page) ProcessFeed(ctx context.Context, data pageDocument) (out feed.Feed, err error) {
	return feed.Feed{
		URL:  data.URL,
		Lang: data.Lang,
		Page: data.Page,
	}, nil
}
