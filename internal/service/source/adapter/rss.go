package adapter

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/mmcdole/gofeed"
)

var _ Adapter[Document] = rss{}

type Document struct {
	URL      string
	Title    string
	Content  string
	Lang     string
	Authors  []string
	Tags     []string
	PostedAt time.Time
}

type rss struct {
	client *http.Client
	fp     *gofeed.Parser
	l      *slog.Logger
}

func NewRSS(l *slog.Logger) Adapter[Document] {
	client := &http.Client{
		Timeout: time.Second * 20,
	}

	fp := gofeed.NewParser()
	fp.Client = client

	return rss{
		client: client,
		fp:     fp,
		l:      l,
	}
}

func (r rss) FetchFromSource(ctx context.Context, url string) (data []Document, err error) {
	f, err := r.fp.ParseURLWithContext(url, ctx)
	if err != nil {
		return
	}

	data = make([]Document, 0, len(f.Items))

	for _, item := range f.Items {
		doc := Document{
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

		data = append(data, doc)
	}

	return data, nil
}

func (r rss) ProcessFeed(ctx context.Context, data Document) (out feed.Feed, err error) {
	out = feed.Feed{
		URL:      data.URL,
		Lang:     data.Lang,
		Title:    data.Title,
		Content:  data.Content,
		Tags:     data.Tags,
		Authors:  data.Authors,
		PostedAt: data.PostedAt,
	}

	if u, err := url.Parse(data.URL); err == nil {
		out.Domain = u.Hostname()
	}

	return out, nil
}
