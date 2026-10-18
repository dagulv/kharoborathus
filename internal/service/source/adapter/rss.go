package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/mmcdole/gofeed"
)

var _ Adapter[rssDocument] = rss{}

type rssDocument struct {
	URL      string
	Title    string
	Content  string
	Lang     string
	Authors  []string
	Tags     []string
	PostedAt time.Time
}

type rss struct {
	client    *http.Client
	feedStore *feed.Store
	l         *slog.Logger
}

func NewRSS(feedStore *feed.Store, l *slog.Logger) Adapter[rssDocument] {
	return rss{
		client:    safehttp.NewClient(time.Second * 20),
		feedStore: feedStore,
		l:         l,
	}
}

func (r rss) FetchFromSource(ctx context.Context, l *slog.Logger, url string) (<-chan rssDocument, error) {
	// A parser per call, since its client logs to this call's logger.
	fp := gofeed.NewParser()
	fp.Client = logs.Client(r.client, l, "rss")

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

func (r rss) Filter(ctx context.Context, data rssDocument) (bool, error) {
	exists, err := r.feedStore.FeedExists(ctx, data.URL)
	if err != nil {
		return false, fmt.Errorf("err feed exists: %w", err)
	}

	return !exists, nil
}

func (r rss) FetchContent(ctx context.Context, _ *slog.Logger, data rssDocument) (rssDocument, error) {
	return data, nil
}

func (r rss) ProcessFeed(ctx context.Context, data rssDocument) (out feed.Feed, err error) {
	out = feed.Feed{
		URL:      data.URL,
		Lang:     data.Lang,
		Title:    data.Title,
		Content:  data.Content,
		Tags:     data.Tags,
		Authors:  data.Authors,
		PostedAt: data.PostedAt,
	}

	return out, nil
}
