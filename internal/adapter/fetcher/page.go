package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
)

var _ port.Fetcher[pageDocument] = page{}

// A url already in raw_posts is not fetched again.
type PostStore interface {
	RawPostExists(ctx context.Context, url string) (bool, error)
}

type Linker interface {
	Links(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte) ([]string, error)
	Next(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte, n int) (string, error)
}

type DomainStore interface {
	GetOrCreateDomain(ctx context.Context, rawURL string) (domain.Domain, error)
}

type pageDocument struct {
	URL  string
	Lang string
	Page []byte
}

type page struct {
	client  *http.Client
	pacer   *pace.Pacer
	posts   PostStore
	domains DomainStore
	linker  Linker
	l       *slog.Logger
}

func NewPage(posts PostStore, domains DomainStore, linker Linker, pacer *pace.Pacer, l *slog.Logger) port.Fetcher[pageDocument] {
	return page{
		client:  safehttp.NewClient(time.Second * 20),
		pacer:   pacer,
		posts:   posts,
		domains: domains,
		linker:  linker,
		l:       l,
	}
}

func (r page) FetchFeed(ctx context.Context, l *slog.Logger, rawURL string) (<-chan pageDocument, error) {
	if err := r.pacer.Wait(ctx, rawURL); err != nil {
		return nil, err
	}

	listing, err := FetchHTML(ctx, logs.Client(r.client, l, "page"), rawURL)
	if err != nil {
		return nil, fmt.Errorf("err fetch listing: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(listing))
	if err != nil {
		return nil, fmt.Errorf("err parse listing: %w", err)
	}

	lang, _ := doc.Find("html").Attr("lang")

	d, err := r.domains.GetOrCreateDomain(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("err get domain: %w", err)
	}

	links, err := r.linker.Links(ctx, l, d, rawURL, listing)
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
	exists, err := r.posts.RawPostExists(ctx, data.URL)
	if err != nil {
		return false, fmt.Errorf("err raw post exists: %w", err)
	}

	return !exists, nil
}

func (r page) FetchPost(ctx context.Context, l *slog.Logger, data pageDocument) (pageDocument, error) {
	if err := r.pacer.Wait(ctx, data.URL); err != nil {
		return data, err
	}

	page, err := FetchHTML(ctx, logs.Client(r.client, l, "page"), data.URL)
	if err != nil {
		return data, fmt.Errorf("err fetch article: %w", err)
	}

	data.Page = page

	return data, nil
}

// The page goes to trim as raw content, which fills the content.
func (r page) ProcessFeed(ctx context.Context, data pageDocument) (domain.RawPost, error) {
	return domain.RawPost{
		URL:        data.URL,
		Lang:       data.Lang,
		RawContent: string(data.Page),
	}, nil
}
