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

var (
	_ port.Fetcher[rssDocument]    = rss{}
	_ port.Backfiller[rssDocument] = rss{}
)

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
	posts  PostStore
	l      *slog.Logger
	// Tried in order, the first that supports the site wins.
	archives []archive
}

func NewRSS(posts PostStore, pacer *pace.Pacer, l *slog.Logger) port.Fetcher[rssDocument] {
	r := rss{
		client: safehttp.NewClient(time.Second * 20),
		pacer:  pacer,
		posts:  posts,
		l:      l,
	}
	r.archives = []archive{r.wordpress}

	return r
}

func (r rss) Backfill(ctx context.Context, l *slog.Logger, url string) (<-chan rssDocument, error) {
	for _, a := range r.archives {
		items, ok, err := a(ctx, l, url)
		if err != nil {
			return nil, err
		}

		if ok {
			return items, nil
		}
	}

	return nil, port.ErrNoArchive
}

func (r rss) FetchFeed(ctx context.Context, l *slog.Logger, url string) (<-chan rssDocument, error) {
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

func (r rss) Filter(ctx context.Context, data rssDocument) (bool, error) {
	exists, err := r.posts.RawPostExists(ctx, data.URL)
	if err != nil {
		return false, fmt.Errorf("err raw post exists: %w", err)
	}

	return !exists, nil
}

// Every item gets its article fetched, since a feed's content may be cut short. A failed fetch keeps the feed's content.
func (r rss) FetchPost(ctx context.Context, l *slog.Logger, data rssDocument) (rssDocument, error) {
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

func (r rss) ProcessFeed(ctx context.Context, data rssDocument) (domain.RawPost, error) {
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

	return out, nil
}
