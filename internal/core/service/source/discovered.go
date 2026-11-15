package source

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
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
// The returned status is the discovery post's.
func (d Discovered) rawPost(ctx context.Context, l *slog.Logger, link postgres.Discovered) (domain.RawPost, string, error) {
	s := d.Service

	p, ok, err := s.RawPostStore.ClaimRawPost(ctx, postgres.Claim{URL: link.URL, DiscoveryPostID: link.ID})
	if err != nil || !ok {
		return p, domain.StatusDuplicate, err
	}

	s.step(ctx, p.ID, domain.StepFoundBy, domain.OutcomePassed, "", map[string]any{
		"discoveryPostId": link.ID,
		"fromPostId":      link.FromPostID,
		"searchQueryId":   link.QueryID,
	}, nil)

	return d.process(ctx, l, p, link.FromPostID)
}

// process takes a claimed raw post from the domain check to its saved content. fromPostID is the
// post the link was found in, credited when the link's source is new.
func (d Discovered) process(ctx context.Context, l *slog.Logger, p domain.RawPost, fromPostID string) (domain.RawPost, string, error) {
	s := d.Service

	// Checked before the fetch, so a blocked domain gets no request.
	domainIn := map[string]any{"domainId": p.DomainID}

	_, blocked, err := s.SourceStore.DomainSource(ctx, p.DomainID)
	if err != nil {
		return p, "", err
	}

	if blocked {
		return d.end(ctx, p, domain.StatusFailed, domain.StepDomainCheck, "domain blocked", domainIn, nil)
	}

	s.step(ctx, p.ID, domain.StepDomainCheck, domain.OutcomePassed, "", domainIn, nil)

	p, err = s.fetch(ctx, l, p)
	if errors.Is(err, pipeline.ErrSkip) {
		return p, domain.StatusFailed, nil
	}

	if err != nil {
		return p, "", err
	}

	// The post is published at the url the link led to, which another raw post may already have.
	if p.FinalURL != "" && p.FinalURL != p.URL {
		moved, err := s.RawPostStore.MoveURL(ctx, p.ID, p.FinalURL)
		if err != nil {
			return p, "", err
		}

		if !moved {
			return d.end(ctx, p, domain.StatusDuplicate, domain.StepDuplicateCheck, "redirects to an already stored url", map[string]any{"finalUrl": p.FinalURL}, nil)
		}

		p.URL = p.FinalURL
	}

	p.Lang = normalizeLang(htmlLang([]byte(p.RawContent)))

	p, err = s.Extract(ctx, l, p)
	if errors.Is(err, pipeline.ErrSkip) {
		return p, domain.StatusRejected, nil
	}

	if err != nil {
		return p, "", err
	}

	dateIn := map[string]any{"postedAt": p.PostedAt, "windowDays": discoveredWindow}

	stale := p.PostedAt != nil && p.PostedAt.Before(time.Now().AddDate(0, 0, -discoveredWindow))

	if stale && !p.Override {
		reason := fmt.Sprintf("posted %s, older than %d days", p.PostedAt.Format(time.DateOnly), discoveredWindow)
		return d.end(ctx, p, domain.StatusRejected, domain.StepDateWindow, reason, dateIn, nil)
	}

	reason := ""
	if stale {
		reason = "manual override"
	}

	s.step(ctx, p.ID, domain.StepDateWindow, domain.OutcomePassed, reason, dateIn, nil)

	p, _ = SetMarkdown(ctx, l, p)
	p, _ = SetSimhash(ctx, l, p)

	source, blocked, err := s.SourceStore.DomainSource(ctx, p.DomainID)
	if err != nil {
		return p, "", err
	}

	matchIn := map[string]any{"domainId": p.DomainID}

	if blocked {
		return d.end(ctx, p, domain.StatusFailed, domain.StepSourceMatch, "source blocked", matchIn, nil)
	}

	proposed := source.ID == ""

	if proposed {
		baseURL, err := domain.BaseURL(cmp.Or(p.FinalURL, p.URL))
		if err != nil {
			return d.end(ctx, p, domain.StatusFailed, domain.StepSourceMatch, "no base url: "+err.Error(), matchIn, nil)
		}

		if source.ID, err = s.SourceStore.CreateProposed(ctx, p.DomainID, baseURL, fromPostID); err != nil {
			return p, "", err
		}
	}

	p.SourceID = source.ID
	if p.Lang == "" {
		p.Lang = source.Lang
	}

	s.step(ctx, p.ID, domain.StepSourceMatch, domain.OutcomePassed, "", matchIn, map[string]any{"sourceId": source.ID, "proposed": proposed})

	if p, err = s.saveContent(ctx, l, p); err != nil {
		return p, "", err
	}

	return p, domain.StatusAccepted, nil
}

// end stops the raw post with status and gives the discovery post's matching status.
func (d Discovered) end(ctx context.Context, p domain.RawPost, status, step, reason string, input, output any) (domain.RawPost, string, error) {
	if err := d.Service.stop(ctx, p, status, step, reason, input, output); !errors.Is(err, pipeline.ErrSkip) {
		return p, "", err
	}

	return p, status, nil
}

func htmlLang(page []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return ""
	}

	lang, _ := doc.Find("html").Attr("lang")

	return lang
}
