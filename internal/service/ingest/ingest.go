package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source"
	sourceadapter "github.com/dagulv/kharoborathus/internal/service/source/adapter"
	"github.com/dagulv/kharoborathus/internal/service/switches"
	"github.com/dagulv/kharoborathus/internal/service/tagging"
	"github.com/jackc/pgx/v5"
)

const (
	tickInterval = time.Minute
	batchSize    = 10
	dailyCap     = 200
	maxAttempts  = 3
	domainGap    = 10 * time.Second
	fetchTimeout = 20 * time.Second
)

var errGate = errors.New("ingest: gate failed")

type domainAllower interface {
	DomainAllowed(ctx context.Context, baseURL string) (bool, error)
}

type Ingester struct {
	store      *Store
	allower    domainAllower
	switches   *switches.Store
	tags       *tagging.Store
	feeds      *feed.Store
	domains    *domain.Store
	trim       *source.Trim
	tagger     *robot.Tagger
	summarizer *robot.Summarizer
	client     *http.Client
	pacer      *pacer
	robots     *Robots
	l          *slog.Logger
	calls      *slog.Logger
}

type result struct {
	status   string
	domainID string
	reason   string
	scores   any
	aiCalls  int
}

func NewIngester(s *Store, sw *switches.Store, tags *tagging.Store, feeds *feed.Store, domains *domain.Store, trim *source.Trim, tagger *robot.Tagger, summarizer *robot.Summarizer, l, calls *slog.Logger) *Ingester {
	client := safehttp.NewClient(fetchTimeout)
	p := newPacer(domainGap)

	return &Ingester{
		store:      s,
		allower:    s,
		switches:   sw,
		tags:       tags,
		feeds:      feeds,
		domains:    domains,
		trim:       trim,
		tagger:     tagger,
		summarizer: summarizer,
		client:     client,
		pacer:      p,
		robots:     NewRobots(client, p),
		l:          l,
		calls:      calls,
	}
}

func (in *Ingester) Run(ctx context.Context) error {
	for range schedule.Always.Tick(ctx, tickInterval) {
		if err := in.tick(ctx); err != nil {
			in.l.Error("ingest links", "err", err)
		}
	}

	return ctx.Err()
}

func (in *Ingester) tick(ctx context.Context) error {
	on, err := in.switches.Enabled(ctx, switches.DiscoveredIngest)
	if err != nil || !on {
		return err
	}

	tags, err := in.tags.ListTags(ctx)
	if err != nil {
		return fmt.Errorf("err list tags: %w", err)
	}

	if !slices.ContainsFunc(tags, func(t robot.Tag) bool { return t.Gate }) {
		in.l.Debug("no gate tags")
		return nil
	}

	checked, err := in.store.CheckedLastDay(ctx)
	if err != nil || checked >= dailyCap {
		return err
	}

	links, err := in.store.ListPending(ctx, pendingWindow, min(batchSize, dailyCap-checked))
	if err != nil {
		return err
	}

	counts := make(map[string]int)
	aiCalls := 0

	defer func() {
		if len(links) > 0 {
			in.l.Info("ingest", "processed", len(links), "counts", counts, "aiCalls", aiCalls)
		}
	}()

	for _, link := range links {
		res, err := in.process(ctx, in.calls.With("link_event_id", link.ID), link, tags)
		aiCalls += res.aiCalls

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			failed, rerr := in.store.Retry(ctx, link.ID, res.domainID, err.Error(), maxAttempts)
			if rerr != nil {
				return rerr
			}

			res.status = "retried"
			if failed {
				res.status = statusFailed
			}

			// A classifier outage would otherwise burn every link of the tick. One attempt per tick keeps a poison page from blocking the queue.
			if errors.Is(err, errGate) {
				counts[res.status]++
				return err
			}
		case res.status != statusAccepted:
			if err := in.store.Finish(ctx, link.ID, res.status, res.domainID, res.scores, res.reason); err != nil {
				return err
			}
		}

		counts[res.status]++
	}

	return nil
}

// process returns the link's final status, or an error to retry it on a later tick.
func (in *Ingester) process(ctx context.Context, l *slog.Logger, link Link, tags []robot.Tag) (res result, err error) {
	base, err := domain.BaseURL(link.URL)
	if err != nil {
		res.status, res.reason = statusFailed, "bad url"
		return res, nil
	}

	// Redirects to another host fail below, so the link's own domain decides before any request.
	allowedDomain, err := in.allower.DomainAllowed(ctx, base)
	if err != nil {
		return res, err
	}

	if !allowedDomain {
		res.status, res.reason = statusFailed, "source disabled"
		return res, nil
	}

	allowed, err := in.robots.Allowed(ctx, l, link.URL)
	if errors.Is(err, safehttp.ErrPrivateAddress) {
		res.status, res.reason = statusFailed, "private address"
		return res, nil
	}

	if err != nil {
		return res, err
	}

	if !allowed {
		res.status, res.reason = statusFailed, "disallowed by robots.txt"
		return res, nil
	}

	if err := in.pacer.Wait(ctx, hostOf(link.URL)); err != nil {
		return res, err
	}

	page, finalURL, err := sourceadapter.FetchPage(ctx, logs.Client(in.client, l, "ingest"), link.URL)
	if err != nil {
		return res, err
	}

	// robots.txt and the pacer covered only the original host.
	if host := hostOf(finalURL); host != hostOf(link.URL) {
		res.status, res.reason = statusFailed, "redirected to "+host
		return res, nil
	}

	d, err := in.domains.GetOrCreateDomain(ctx, finalURL)
	if err != nil {
		return res, fmt.Errorf("err get domain: %w", err)
	}

	res.domainID = d.ID

	f := in.trim.Apply(ctx, l, d, feed.Feed{URL: finalURL, DomainID: d.ID, Lang: pageLang(page)}, page)

	if f.Content == "" {
		res.status, res.reason = statusRejected, "no content"
		return res, nil
	}

	if f.Title == "" {
		res.status, res.reason = statusRejected, "no title"
		return res, nil
	}

	// An old article linked from fresh news would otherwise show up as current, since a missing date becomes now.
	if !f.PostedAt.IsZero() && time.Since(f.PostedAt) > pendingWindow*24*time.Hour {
		res.status, res.reason = statusRejected, "stale"
		return res, nil
	}

	f, err = source.Markdown(f)
	if err != nil {
		return res, err
	}

	duplicate, err := in.feeds.HasDuplicate(ctx, f.Content)
	if err != nil {
		return res, err
	}

	if duplicate {
		res.status = statusDuplicate
		return res, nil
	}

	res.aiCalls++

	v, err := in.tagger.Gate(ctx, l, f.Title, f.Content, tags)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errGate, err)
	}

	res.scores = v

	if !v.Passed {
		res.status = statusRejected
		return res, nil
	}

	res.aiCalls++

	if summary, err := in.summarizer.Summarize(ctx, l, f.Content); err == nil {
		f.ShortContent = summary
	} else if !errors.Is(err, adapter.ErrUnavailable) {
		in.l.Warn("summarize", "url", link.URL, "err", err)
	}

	f.Tags = v.Keys
	f.Status = feed.StatusActive

	_, err = in.store.Accept(ctx, link, d.BaseURL, f, v)
	if errors.Is(err, errSourceDisabled) {
		res.status, res.reason = statusFailed, "source disabled"
		return res, nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		res.status, res.reason = statusDuplicate, "url exists"
		return res, nil
	}

	if err != nil {
		return res, err
	}

	res.status = statusAccepted

	return res, nil
}

func pageLang(page []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return ""
	}

	lang, _ := doc.Find("html").Attr("lang")

	return lang
}
