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

var _ port.Backfiller = page{}

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
}

type page struct {
	client  *http.Client
	pacer   *pace.Pacer
	store   PostStore
	domains DomainStore
	linker  Linker
	l       *slog.Logger
}

func NewPage(store PostStore, domains DomainStore, linker Linker, pacer *pace.Pacer, l *slog.Logger) port.Backfiller {
	return page{
		client:  safehttp.NewClient(time.Second * 20),
		pacer:   pacer,
		store:   store,
		domains: domains,
		linker:  linker,
		l:       l,
	}
}

func (r page) Fetch(ctx context.Context, l *slog.Logger, rawURL string) (<-chan domain.RawPost, error) {
	links, err := r.links(ctx, l, rawURL)
	if err != nil {
		return nil, err
	}

	return r.posts(ctx, l, links), nil
}

func (r page) Backfill(ctx context.Context, l *slog.Logger, rawURL string) (<-chan domain.RawPost, error) {
	links, err := r.history(ctx, l, rawURL)
	if err != nil {
		return nil, err
	}

	return r.posts(ctx, l, links), nil
}

// posts skips stored links and fetches each article. The page goes to extraction as raw content.
func (r page) posts(ctx context.Context, l *slog.Logger, links <-chan pageDocument) <-chan domain.RawPost {
	out := make(chan domain.RawPost)

	go func() {
		defer close(out)

		for link := range links {
			exists, err := r.store.RawPostExists(ctx, link.URL)
			if err != nil {
				r.l.Error("raw post exists", "url", link.URL, "err", err)
				continue
			}

			if exists {
				continue
			}

			if err := r.pacer.Wait(ctx, link.URL); err != nil {
				return
			}

			body, err := FetchHTML(ctx, logs.Client(r.client, l, "page"), link.URL)
			if err != nil {
				r.l.Debug("fetch article", "url", link.URL, "err", err)
				continue
			}

			select {
			case out <- domain.RawPost{URL: link.URL, Lang: link.Lang, RawContent: string(body)}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func (r page) links(ctx context.Context, l *slog.Logger, rawURL string) (<-chan pageDocument, error) {
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
