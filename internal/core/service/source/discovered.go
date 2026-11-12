package source

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/PuerkitoBio/goquery"
	fetchers "github.com/dagulv/kharoborathus/internal/adapter/fetcher"
	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

const (
	DiscoveredQueue = 100
	// Older links and pages are stale news.
	discoveredWindow = 7
	discoveredBatch  = 20
)

// Turns discovery_posts into raw posts with step 1's stages.
type Discovered struct {
	Service *Service
	Store   *postgres.DiscoveredStore
	Client  *http.Client
	Pacer   *pace.Pacer
	// Third-party requests, one row each in the logs table.
	Calls *slog.Logger
}

// One link at a time.
func (d Discovered) Run(ctx context.Context, ids <-chan string, l *slog.Logger) error {
	tick := d.Service.Schedule.Tick(ctx, time.Minute)

	for {
		select {
		case id := <-ids:
			if err := d.ingest(ctx, l, id); err != nil && ctx.Err() == nil {
				l.Error("ingest", "discovery_post", id, "err", err)
			}
		case <-tick:
			if err := d.catchUp(ctx, l); err != nil && ctx.Err() == nil {
				l.Error("ingest catch up", "err", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Stops at the first error, so an outage costs one call a tick instead of a whole batch.
func (d Discovered) catchUp(ctx context.Context, l *slog.Logger) error {
	ids, err := d.Store.ListPending(ctx, discoveredWindow, discoveredBatch)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := d.ingest(ctx, l, id); err != nil {
			return err
		}
	}

	return nil
}

func (d Discovered) ingest(ctx context.Context, l *slog.Logger, id string) error {
	link, found, err := d.Store.GetPending(ctx, id, discoveredWindow)
	if err != nil || !found {
		return err
	}

	l = l.With("discovery_post", id, "url", link.URL)

	p, status, err := d.rawPost(ctx, l, link)
	if err != nil {
		return err
	}

	if err := d.Store.SetStatus(ctx, id, status); err != nil || status != domain.StatusAccepted {
		return err
	}

	p, err = d.Service.checkQuality(ctx, l, p)
	if errors.Is(err, pipeline.ErrSkip) {
		return nil
	}

	if err != nil {
		return err
	}

	return d.Service.saveStatus(ctx, l, p)
}

// An error leaves the link pending for the catch-up. Every status is final.
func (d Discovered) rawPost(ctx context.Context, l *slog.Logger, link postgres.Discovered) (domain.RawPost, string, error) {
	exists, err := d.Service.RawPostStore.RawPostExists(ctx, link.URL)
	if err != nil || exists {
		return domain.RawPost{}, domain.StatusDuplicate, err
	}

	// Checked before the fetch, so a blocked domain gets no request.
	dom, err := d.Service.DomainStore.GetOrCreateDomain(ctx, link.URL)
	if err != nil {
		return domain.RawPost{}, "", err
	}

	if _, blocked, err := d.Service.SourceStore.DomainSource(ctx, dom.ID); err != nil || blocked {
		return domain.RawPost{}, domain.StatusFailed, err
	}

	if err := d.Pacer.Wait(ctx, link.URL); err != nil {
		return domain.RawPost{}, "", err
	}

	page, finalURL, err := fetchers.FetchPage(ctx, logs.Client(d.Client, d.Calls.With("discovery_post_id", link.ID), "ingest"), link.URL)
	if ctx.Err() != nil {
		return domain.RawPost{}, "", ctx.Err()
	}

	if err != nil {
		l.Debug("fetch", "err", err)
		return domain.RawPost{}, domain.StatusFailed, nil
	}

	// A redirect can leave the host, so the domain and its source come from the final url.
	p, err := d.Service.Extract(ctx, l, domain.RawPost{URL: finalURL, Lang: normalizeLang(htmlLang(page)), RawContent: string(page)})
	if errors.Is(err, pipeline.ErrSkip) {
		return p, domain.StatusRejected, nil
	}

	if err != nil {
		return p, "", err
	}

	if p.PostedAt != nil && p.PostedAt.Before(time.Now().AddDate(0, 0, -discoveredWindow)) {
		return p, domain.StatusRejected, nil
	}

	p, _ = SetMarkdown(ctx, l, p)
	p, _ = SetSimhash(ctx, l, p)

	source, blocked, err := d.Service.SourceStore.DomainSource(ctx, p.DomainID)
	if err != nil || blocked {
		return p, domain.StatusFailed, err
	}

	if source.ID == "" {
		baseURL, err := domain.BaseURL(finalURL)
		if err != nil {
			return p, domain.StatusFailed, nil
		}

		if source.ID, err = d.Service.SourceStore.CreateProposed(ctx, p.DomainID, baseURL, link.FromPostID); err != nil {
			return p, "", err
		}
	}

	p.SourceID = source.ID
	if p.Lang == "" {
		p.Lang = source.Lang
	}

	p, err = d.Service.insert(ctx, l, p)
	if errors.Is(err, pipeline.ErrSkip) {
		return p, domain.StatusDuplicate, nil
	}

	if err != nil {
		return p, "", err
	}

	return p, domain.StatusAccepted, nil
}

func htmlLang(page []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return ""
	}

	lang, _ := doc.Find("html").Attr("lang")

	return lang
}
