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

type Ingester struct {
	store      *Store
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
}

type result struct {
	status   string
	domainID string
	reason   string
	scores   any
	aiCalls  int
}

func NewIngester(s *Store, sw *switches.Store, tags *tagging.Store, feeds *feed.Store, domains *domain.Store, trim *source.Trim, tagger *robot.Tagger, summarizer *robot.Summarizer, l *slog.Logger) *Ingester {
	client := safeClient(fetchTimeout)
	p := newPacer(domainGap)

	return &Ingester{
		store:      s,
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
		res, err := in.process(ctx, link, tags)
		aiCalls += res.aiCalls

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			failed, err := in.store.Retry(ctx, link.ID, res.domainID, err.Error(), maxAttempts)
			if err != nil {
				return err
			}

			res.status = "retried"
			if failed {
				res.status = statusFailed
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
func (in *Ingester) process(ctx context.Context, link Link, tags []robot.Tag) (res result, err error) {
	allowed, err := in.robots.Allowed(ctx, link.URL)
	if errors.Is(err, errPrivateAddress) {
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

	page, err := sourceadapter.FetchHTML(ctx, in.client, link.URL)
	if err != nil {
		return res, err
	}

	d, err := in.domains.GetOrCreateDomain(ctx, link.URL)
	if err != nil {
		return res, fmt.Errorf("err get domain: %w", err)
	}

	res.domainID = d.ID

	f := in.trim.Apply(ctx, d, feed.Feed{URL: link.URL, DomainID: d.ID, Lang: pageLang(page)}, page)

	if f.Content == "" {
		res.status, res.reason = statusRejected, "no content"
		return res, nil
	}

	if f.Title == "" {
		res.status, res.reason = statusRejected, "no title"
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

	v, err := in.tagger.Gate(ctx, f.Title, f.Content, tags)
	if err != nil {
		return res, fmt.Errorf("err gate: %w", err)
	}

	res.scores = v

	if !v.Passed {
		res.status = statusRejected
		return res, nil
	}

	res.aiCalls++

	if summary, err := in.summarizer.Summarize(ctx, f.Content); err == nil {
		f.ShortContent = summary
	} else if !errors.Is(err, adapter.ErrUnavailable) {
		in.l.Warn("summarize", "url", link.URL, "err", err)
	}

	f.Tags = v.Keys

	_, err = in.store.Accept(ctx, link, f, v)
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
