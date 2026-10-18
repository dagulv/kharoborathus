package adapter

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/service/feed"
)

var _ Adapter[pageDocument] = page{}

type Linker interface {
	Links(ctx context.Context, l *slog.Logger, url string, listing []byte) ([]string, error)
}

type pageDocument struct {
	URL  string
	Lang string
	Page []byte
}

type page struct {
	client    *http.Client
	feedStore *feed.Store
	linker    Linker
	l         *slog.Logger
}

func NewPage(feedStore *feed.Store, linker Linker, l *slog.Logger) Adapter[pageDocument] {
	return page{
		client:    safehttp.NewClient(time.Second * 20),
		feedStore: feedStore,
		linker:    linker,
		l:         l,
	}
}

func (r page) FetchFromSource(ctx context.Context, l *slog.Logger, rawURL string) (<-chan pageDocument, error) {
	listing, err := FetchHTML(ctx, logs.Client(r.client, l, "page"), rawURL)
	if err != nil {
		return nil, fmt.Errorf("err fetch listing: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(listing))
	if err != nil {
		return nil, fmt.Errorf("err parse listing: %w", err)
	}

	lang, _ := doc.Find("html").Attr("lang")

	links, err := r.linker.Links(ctx, l, rawURL, listing)
	if err != nil {
		return nil, fmt.Errorf("err links: %w", err)
	}

	out := make(chan pageDocument)

	go func() {
		defer close(out)

		for _, link := range links {
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

func (r page) FetchContent(ctx context.Context, l *slog.Logger, data pageDocument) (pageDocument, error) {
	page, err := FetchHTML(ctx, logs.Client(r.client, l, "page"), data.URL)
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
