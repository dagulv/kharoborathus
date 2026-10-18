package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/dagulv/kharoborathus/internal/lib/iterx"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	robotadapter "github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/adapter"
	"golang.org/x/net/html"
)

const (
	workers         = 5
	tickInterval    = time.Second
	refreshInterval = time.Minute
	sourcePage      = 200
	excerptLen      = 280

	maxFetchFailures = 10
	minFailing       = 24 * time.Hour
	probeInterval    = 7 * 24 * time.Hour
)

type Pipeline interface {
	Run(ctx context.Context, l *slog.Logger) <-chan feed.Feed
}

func initPipelines(ctx context.Context, in <-chan Source, s *Store, trim *Trim, summarizer *robot.Summarizer, l, calls *slog.Logger) []Pipeline {
	sinks := make(map[string]chan Source)

	pipelines := []Pipeline{
		newPipeline(adapter.NewRSS(&s.feedStore, l), addSink(sinks, "rss"), s, trim, summarizer, calls),
		newPipeline(adapter.NewPage(&s.feedStore, trim, l), addSink(sinks, "page"), s, trim, summarizer, calls),
		newPipeline(adapter.NewWordPress(&s.feedStore, l), addSink(sinks, "wordpress"), s, trim, summarizer, calls),
	}

	go func() {
		defer func() {
			for _, c := range sinks {
				close(c)
			}
		}()

		for s := range in {
			c, ok := sinks[s.Adapter]
			if !ok {
				l.Error("unknown adapter", "adapter", s.Adapter, "source", s.ID)
				continue
			}

			select {
			case c <- s:
			case <-ctx.Done():
				return
			}
		}
	}()

	return pipelines
}

func addSink(sinks map[string]chan Source, name string) <-chan Source {
	c := make(chan Source)
	sinks[name] = c

	return c
}

func Run(ctx context.Context, s *Store, syncs <-chan Source, trim *Trim, summarizer *robot.Summarizer, sched schedule.Schedule, l, calls *slog.Logger) error {
	sources := merge(ctx, continuousSources(ctx, s, syncs, sched, l), backfills(ctx, s, sched, l, calls))

	pipelines := initPipelines(ctx, sources, s, trim, summarizer, l, calls)

	feeds := make([]<-chan feed.Feed, 0, len(pipelines))

	for _, p := range pipelines {
		feeds = append(feeds, p.Run(ctx, l))
	}

	for f := range merge(ctx, feeds...) {
		_, err := s.feedStore.CreateFeed(ctx, f)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			l.Error("create feed", "source", f.SourceID, "url", f.URL, "err", err)
			continue
		}

		if err := s.Revive(ctx, f.SourceID); err != nil {
			l.Error("revive source", "source", f.SourceID, "err", err)
		}
	}

	return ctx.Err()
}

func continuousSources(ctx context.Context, s *Store, syncs <-chan Source, sched schedule.Schedule, l *slog.Logger) <-chan Source {
	out := make(chan Source)

	go func() {
		defer close(out)

		tick := sched.Tick(ctx, tickInterval)

		paused := make(map[string]time.Time)
		var lastInterval time.Time
		sources := make([]Source, 0)

		dispatch := func(source Source) bool {
			select {
			case out <- source:
				paused[source.ID] = time.Now().Add(time.Duration(source.Rate) * time.Minute)
				return true
			case <-ctx.Done():
				return false
			}
		}

		for {
			select {
			case <-ctx.Done():
				return

			// Manual sync skips the rate and restarts the source's wait from now
			case source, ok := <-syncs:
				if !ok {
					syncs = nil
					continue
				}

				if !dispatch(source) {
					return
				}

			case _, ok := <-tick:
				if !ok {
					return
				}

				if time.Since(lastInterval) > refreshInterval {
					lastInterval = time.Now()

					if enabled, err := enabledSources(ctx, s); err != nil {
						l.Error("unable to get sources", "err", err)
					} else {
						sources = enabled
						clipPauses(paused, sources, time.Now())
					}
				}

				for _, source := range sources {
					if pause, ok := paused[source.ID]; ok && time.Now().Before(pause) {
						continue
					}

					if !dispatch(source) {
						return
					}
				}
			}
		}
	}()

	return out
}

// A revived source must not keep the week-long pause of the probe that revived it.
func clipPauses(paused map[string]time.Time, sources []Source, now time.Time) {
	for _, source := range sources {
		limit := now.Add(time.Duration(source.Rate) * time.Minute)

		if pause, ok := paused[source.ID]; ok && pause.After(limit) {
			paused[source.ID] = limit
		}
	}
}

func enabledSources(ctx context.Context, s *Store) ([]Source, error) {
	var sources []Source

	for _, status := range []string{StatusActive, StatusDormant} {
		for offset := 0; ; offset += sourcePage {
			page, err := iterx.Collect(s.ListSources(ctx, Filter{
				Limit:   strconv.Itoa(sourcePage),
				Offset:  strconv.Itoa(offset),
				OrderBy: "id",
				Enabled: sql.Null[bool]{V: true, Valid: true},
				Status:  sql.Null[string]{V: status, Valid: true},
			}))
			if err != nil {
				return nil, fmt.Errorf("err list %s sources: %w", status, err)
			}

			for _, source := range page {
				if source.Status == StatusDormant {
					source.Rate = int(probeInterval / time.Minute)
				}

				sources = append(sources, source)
			}

			if len(page) < sourcePage {
				break
			}
		}
	}

	return sources, nil
}

type fetched[T any] struct {
	source Source
	data   T
}

type resolved struct {
	feed   feed.Feed
	domain domain.Domain
}

type pipeline[T any] struct {
	adapter.Adapter[T]

	in         <-chan Source
	store      *Store
	trim       *Trim
	summarizer *robot.Summarizer
	calls      *slog.Logger
}

func newPipeline[T any](a adapter.Adapter[T], in <-chan Source, store *Store, trim *Trim, summarizer *robot.Summarizer, calls *slog.Logger) Pipeline {
	return pipeline[T]{Adapter: a, in: in, store: store, trim: trim, summarizer: summarizer, calls: calls}
}

func (p pipeline[T]) Run(ctx context.Context, l *slog.Logger) <-chan feed.Feed {
	data := fanOut(ctx, workers, p.in, l, p.fetchFromSource)
	filtered := fanOut(ctx, workers, data, l, p.filter)
	content := fanOut(ctx, workers, filtered, l, p.fetchContent)
	feeds := fanOut(ctx, workers, content, l, p.processFeed)
	fresh := fanOut(ctx, workers, feeds, l, p.resolveFeed)
	trimmed := fanOut(ctx, workers, fresh, l, p.trimFeed)
	converted := fanOut(ctx, workers, trimmed, l, p.markdownFeed)

	return fanOut(ctx, workers, converted, l, p.summarizeFeed)
}

func (p pipeline[T]) fetchFromSource(ctx context.Context, l *slog.Logger, in <-chan Source) <-chan fetched[T] {
	out := make(chan fetched[T])

	go func() {
		defer close(out)

		for source := range in {
			dataset, err := p.FetchFromSource(ctx, p.calls.With("source_id", source.ID), source.URL)
			if rerr := p.store.RecordFetch(ctx, source.ID, err == nil, maxFetchFailures, minFailing); rerr != nil {
				l.Error("record fetch", "source", source.ID, "err", rerr)
			}

			if err != nil {
				l.Error("fetchFromSource", "source", source.ID, "url", source.URL, "err", err)
				continue
			}

			for data := range dataset {
				select {
				case out <- fetched[T]{source: source, data: data}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out
}

func (p pipeline[T]) filter(ctx context.Context, l *slog.Logger, in <-chan fetched[T]) <-chan fetched[T] {
	out := make(chan fetched[T])

	go func() {
		defer close(out)

		for f := range in {
			keep, err := p.Filter(ctx, f.data)
			if err != nil {
				l.Error("filter", "source", f.source.ID, "url", f.source.URL, "err", err)
				continue
			}

			if !keep {
				continue
			}

			select {
			case out <- f:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func (p pipeline[T]) fetchContent(ctx context.Context, l *slog.Logger, in <-chan fetched[T]) <-chan fetched[T] {
	out := make(chan fetched[T])

	go func() {
		defer close(out)

		for f := range in {
			data, err := p.FetchContent(ctx, p.calls.With("source_id", f.source.ID), f.data)
			if err != nil {
				l.Debug("fetchContent", "source", f.source.ID, "url", f.source.URL, "err", err)
				continue
			}

			select {
			case out <- fetched[T]{source: f.source, data: data}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func (p pipeline[T]) processFeed(ctx context.Context, l *slog.Logger, in <-chan fetched[T]) <-chan feed.Feed {
	out := make(chan feed.Feed)

	go func() {
		defer close(out)

		for f := range in {
			parsed, err := p.ProcessFeed(ctx, f.data)
			if err != nil {
				l.Error("processFeed", "source", f.source.ID, "url", f.source.URL, "err", err)
				continue
			}

			parsed.SourceID = f.source.ID

			if parsed.Lang == "" {
				parsed.Lang = f.source.Lang
			}

			select {
			case out <- parsed:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func (p pipeline[T]) resolveFeed(ctx context.Context, l *slog.Logger, in <-chan feed.Feed) <-chan resolved {
	out := make(chan resolved)

	go func() {
		defer close(out)

		for f := range in {
			d, err := p.store.domainStore.GetOrCreateDomain(ctx, f.URL)
			if err != nil {
				l.Error("get domain", "source", f.SourceID, "url", f.URL, "err", err)
				continue
			}

			f.DomainID = d.ID

			select {
			case out <- resolved{feed: f, domain: d}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func (p pipeline[T]) trimFeed(ctx context.Context, l *slog.Logger, in <-chan resolved) <-chan feed.Feed {
	out := make(chan feed.Feed)

	go func() {
		defer close(out)

		for r := range in {
			f := r.feed

			if f.Page != nil {
				f = p.trim.Apply(ctx, p.calls.With("source_id", f.SourceID), r.domain, f, f.Page)
			}

			if f.Content == "" {
				l.Debug("no content", "source", f.SourceID, "url", f.URL)
				continue
			}

			select {
			case out <- f:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func (p pipeline[T]) markdownFeed(ctx context.Context, l *slog.Logger, in <-chan feed.Feed) <-chan feed.Feed {
	out := make(chan feed.Feed)

	go func() {
		defer close(out)

		for f := range in {
			md, err := Markdown(f)
			if err != nil {
				l.Error("convert to markdown", "source", f.SourceID, "url", f.URL, "err", err)
			}

			select {
			case out <- md:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func Markdown(f feed.Feed) (feed.Feed, error) {
	if f.ShortContent == "" {
		f.ShortContent = excerpt(f.Content)
	}

	md, err := htmltomarkdown.ConvertString(f.Content, converter.WithDomain(f.URL))
	if err != nil {
		return f, fmt.Errorf("err convert to markdown: %w", err)
	}

	f.Content = md

	return f, nil
}

func (p pipeline[T]) summarizeFeed(ctx context.Context, l *slog.Logger, in <-chan feed.Feed) <-chan feed.Feed {
	out := make(chan feed.Feed)

	go func() {
		defer close(out)

		for f := range in {
			if !f.SummaryPending {
				if summary, err := p.summarizer.Summarize(ctx, p.calls.With("source_id", f.SourceID, "url", f.URL), f.Content); err == nil {
					f.ShortContent = summary
				} else if errors.Is(err, robotadapter.ErrUnavailable) {
					l.Debug("summarize", "source", f.SourceID, "url", f.URL, "err", err)
				} else {
					l.Warn("summarize", "source", f.SourceID, "url", f.URL, "err", err)
				}
			}

			select {
			case out <- f:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func fanOut[In, Out any](ctx context.Context, n int, in <-chan In, l *slog.Logger, stage func(context.Context, *slog.Logger, <-chan In) <-chan Out) <-chan Out {
	outs := make([]<-chan Out, n)

	for i := range outs {
		outs[i] = stage(ctx, l, in)
	}

	return merge(ctx, outs...)
}

func merge[T any](ctx context.Context, chans ...<-chan T) <-chan T {
	out := make(chan T)

	var wg sync.WaitGroup
	wg.Add(len(chans))

	for _, c := range chans {
		go func() {
			defer wg.Done()

			for v := range c {
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

func excerpt(content string) string {
	text := strings.Join(strings.Fields(htmlText(content)), " ")

	if utf8.RuneCountInString(text) <= excerptLen {
		return text
	}

	r := []rune(text)[:excerptLen]

	if i := strings.LastIndexByte(string(r), ' '); i > 0 {
		return string(r)[:i] + "…"
	}

	return string(r) + "…"
}

func htmlText(content string) string {
	var b strings.Builder

	z := html.NewTokenizer(strings.NewReader(content))

	for {
		switch z.Next() {
		case html.ErrorToken:
			return b.String()
		case html.TextToken:
			b.Write(z.Text())
			b.WriteByte(' ')
		}
	}
}
