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
	domains DomainStore
	linker  Linker
}

func NewPage(domains DomainStore, linker Linker, pacer *pace.Pacer) port.Backfiller {
	return page{
		client:  safehttp.NewClient(time.Second * 20),
		pacer:   pacer,
		domains: domains,
		linker:  linker,
	}
}

func (r page) Fetch(ctx context.Context, l *slog.Logger, rawURL string) (<-chan domain.RawPost, error) {
	links, err := r.links(ctx, l, rawURL)
	if err != nil {
		return nil, err
	}

	return r.posts(ctx, links), nil
}

func (r page) Backfill(ctx context.Context, l *slog.Logger, rawURL string) (<-chan domain.RawPost, error) {
	links, err := r.history(ctx, l, rawURL)
	if err != nil {
		return nil, err
	}

	return r.posts(ctx, links), nil
}

// The pipeline claims and fetches each article.
func (r page) posts(ctx context.Context, links <-chan pageDocument) <-chan domain.RawPost {
	out := make(chan domain.RawPost)

	go func() {
		defer close(out)

		for link := range links {
			select {
			case out <- domain.RawPost{URL: link.URL, Lang: link.Lang}:
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
