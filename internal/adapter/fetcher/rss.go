package fetcher

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/mmcdole/gofeed"
)

var _ port.Backfiller = rss{}

type rssDocument struct {
	URL      string
	Title    string
	Content  string
	Lang     string
	Authors  []string
	Tags     []string
	PostedAt time.Time
	Page     []byte
}

type rss struct {
	client *http.Client
	pacer  *pace.Pacer
	store  PostStore
	l      *slog.Logger
}

func NewRSS(store PostStore, pacer *pace.Pacer, l *slog.Logger) port.Backfiller {
	return rss{
		client: safehttp.NewClient(time.Second * 20),
		pacer:  pacer,
		store:  store,
		l:      l,
	}
}

func (r rss) Fetch(ctx context.Context, l *slog.Logger, url string) (<-chan domain.RawPost, error) {
	items, err := r.feed(ctx, l, url)
	if err != nil {
		return nil, err
	}

	return r.posts(ctx, l, items), nil
}

func (r rss) Backfill(ctx context.Context, l *slog.Logger, url string) (<-chan domain.RawPost, error) {
	items, ok, err := r.wordpress(ctx, l, url)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, port.ErrNoArchive
	}

	return r.posts(ctx, l, items), nil
}

// posts skips stored items and fetches every article, since a feed's content may be cut short.
func (r rss) posts(ctx context.Context, l *slog.Logger, items <-chan rssDocument) <-chan domain.RawPost {
	out := make(chan domain.RawPost)

	go func() {
		defer close(out)

		for item := range items {
			exists, err := r.store.RawPostExists(ctx, item.URL)
			if err != nil {
				r.l.Error("raw post exists", "url", item.URL, "err", err)
				continue
			}

			if exists {
				continue
			}

			item, err = r.article(ctx, l, item)
			if err != nil {
				return
			}

			select {
			case out <- item.rawPost():
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func (r rss) feed(ctx context.Context, l *slog.Logger, url string) (<-chan rssDocument, error) {
	// A parser per call, since its client logs to this call's logger.
	fp := gofeed.NewParser()
	fp.Client = logs.Client(r.client, l, "rss")

	if err := r.pacer.Wait(ctx, url); err != nil {
		return nil, err
	}

	f, err := fp.ParseURLWithContext(url, ctx)
	if err != nil {
		return nil, fmt.Errorf("err parse rss: %w", err)
	}

	out := make(chan rssDocument)

	go func() {
		defer close(out)

		for _, item := range f.Items {
			doc := rssDocument{
				URL:     item.Link,
				Title:   item.Title,
				Content: item.Content,
				Lang:    f.Language,
				Tags:    item.Categories,
				Authors: make([]string, 0, len(item.Authors)),
			}

			if doc.Content == "" {
				doc.Content = item.Description
			}

			if item.PublishedParsed != nil {
				doc.PostedAt = *item.PublishedParsed
			}

			for _, author := range item.Authors {
				if author == nil || author.Name == "" {
					continue
				}

				doc.Authors = append(doc.Authors, author.Name)
			}

			select {
			case out <- doc:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

// A failed fetch keeps the feed's content. Only a shutdown is an error.
func (r rss) article(ctx context.Context, l *slog.Logger, data rssDocument) (rssDocument, error) {
	if err := r.pacer.Wait(ctx, data.URL); err != nil {
		return data, err
	}

	page, err := FetchHTML(ctx, logs.Client(r.client, l, "page"), data.URL)
	if err != nil {
		l.Debug("fetch article", "url", data.URL, "err", err)
		return data, nil
	}

	data.Page = page

	return data, nil
}

func (data rssDocument) rawPost() domain.RawPost {
	out := domain.RawPost{
		URL:        data.URL,
		Lang:       data.Lang,
		Title:      data.Title,
		Content:    data.Content,
		Authors:    data.Authors,
		RawContent: string(data.Page),
	}

	if !data.PostedAt.IsZero() {
		out.PostedAt = &data.PostedAt
	}

	return out
}
