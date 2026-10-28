package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/dagulv/kharoborathus/internal/lib/iterx"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/source/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/model"
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

	domainInterval = 5 * time.Second
)

type Fetcher interface {
	Run(ctx context.Context, l *slog.Logger) <-chan feed.Feed
}

func initFetchers(ctx context.Context, in <-chan model.Source, s *Store, trim *Trim, pacer *pace.Pacer, l, calls *slog.Logger) []Fetcher {
	ins := make(map[string]chan model.Source)

	fetchers := []Fetcher{
		newFetcher(adapter.NewRSS(&s.feedStore, pacer, l), addIn(ins, "rss"), s, calls),
		newFetcher(adapter.NewPage(&s.feedStore, trim, pacer, l), addIn(ins, "page"), s, calls),
		newFetcher(adapter.NewWordPress(&s.feedStore, pacer, l), addIn(ins, "wordpress"), s, calls),
	}

	go func() {
		defer func() {
			for _, c := range ins {
				close(c)
			}
		}()

		for s := range in {
			c, ok := ins[s.Adapter]
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

	return fetchers
}

func addIn(sinks map[string]chan model.Source, name string) <-chan model.Source {
	c := make(chan model.Source)
	sinks[name] = c

	return c
}

func Run(ctx context.Context, s *Store, syncs <-chan model.Source, trim *Trim, sched schedule.Schedule, l, calls *slog.Logger) error {
	pacer := pace.New(domainInterval)

	sources := pipeline.Merge(ctx, continuousSources(ctx, s, syncs, sched, l), backfills(ctx, s, sched, pacer, l, calls))

	fetchers := initFetchers(ctx, sources, s, trim, pacer, l, calls)

	feeds := make([]<-chan feed.Feed, 0, len(fetchers))

	for _, f := range fetchers {
		feeds = append(feeds, f.Run(ctx, l))
	}

	p := processor{store: s, trim: trim, calls: calls}

	pipe := pipeline.New[feed.Feed]().
		Next(workers, p.process).
		WithSink(p.save)

	return pipe.Run(ctx, l, pipeline.Merge(ctx, feeds...))
}

func continuousSources(ctx context.Context, s *Store, syncs <-chan model.Source, sched schedule.Schedule, l *slog.Logger) <-chan model.Source {
	out := make(chan model.Source)

	go func() {
		defer close(out)

		tick := sched.Tick(ctx, tickInterval)

		paused := make(map[string]time.Time)
		var lastInterval time.Time
		sources := make([]model.Source, 0)

		dispatch := func(source model.Source) bool {
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
func clipPauses(paused map[string]time.Time, sources []model.Source, now time.Time) {
	for _, source := range sources {
		limit := now.Add(time.Duration(source.Rate) * time.Minute)

		if pause, ok := paused[source.ID]; ok && pause.After(limit) {
			paused[source.ID] = limit
		}
	}
}

func enabledSources(ctx context.Context, s *Store) ([]model.Source, error) {
	var sources []model.Source

	for _, status := range []string{model.StatusActive, model.StatusDormant} {
		for offset := 0; ; offset += sourcePage {
			page, err := iterx.Collect(s.ListSources(ctx, model.Filter{
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
				if source.Status == model.StatusDormant {
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
	source model.Source
	data   T
}

type fetcher[T any] struct {
	adapter.Adapter[T]

	in    <-chan model.Source
	store *Store
	calls *slog.Logger
}

func newFetcher[T any](a adapter.Adapter[T], in <-chan model.Source, store *Store, calls *slog.Logger) Fetcher {
	return fetcher[T]{Adapter: a, in: in, store: store, calls: calls}
}

func (p fetcher[T]) Run(ctx context.Context, l *slog.Logger) <-chan feed.Feed {
	return pipeline.New[model.Source]().
		NextCh(workers, p.fetchFromSource).
		Next(workers, p.fetchFeed).
		Stream(ctx, l, p.in)
}

func (p fetcher[T]) fetchFromSource(ctx context.Context, l *slog.Logger, in <-chan model.Source) <-chan fetched[T] {
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
				keep, err := p.Filter(ctx, data)
				if err != nil {
					l.Error("filter", "source", source.ID, "url", source.URL, "err", err)
					continue
				}

				if !keep {
					continue
				}

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

func (p fetcher[T]) fetchFeed(ctx context.Context, l *slog.Logger, f fetched[T]) (feed.Feed, error) {
	data, err := p.FetchContent(ctx, p.calls.With("source_id", f.source.ID), f.data)
	if err != nil {
		l.Debug("fetchContent", "source", f.source.ID, "url", f.source.URL, "err", err)
		return feed.Feed{}, pipeline.ErrSkip
	}

	parsed, err := p.ProcessFeed(ctx, data)
	if err != nil {
		l.Error("processFeed", "source", f.source.ID, "url", f.source.URL, "err", err)
		return feed.Feed{}, pipeline.ErrSkip
	}

	parsed.SourceID = f.source.ID

	if parsed.Lang == "" {
		parsed.Lang = f.source.Lang
	}

	return parsed, nil
}

type processor struct {
	store *Store
	trim  *Trim
	calls *slog.Logger
}

func (p processor) process(ctx context.Context, l *slog.Logger, f feed.Feed) (feed.Feed, error) {
	d, err := p.store.domainStore.GetOrCreateDomain(ctx, f.URL)
	if err != nil {
		l.Error("get domain", "source", f.SourceID, "url", f.URL, "err", err)
		return feed.Feed{}, pipeline.ErrSkip
	}

	f.DomainID = d.ID

	if f.Page != nil {
		f = p.trim.Apply(ctx, p.calls.With("source_id", f.SourceID), d, f, f.Page)
	}

	if f.Content == "" {
		l.Debug("no content", "source", f.SourceID, "url", f.URL)
		return feed.Feed{}, pipeline.ErrSkip
	}

	md, err := Markdown(f)
	if err != nil {
		l.Error("convert to markdown", "source", f.SourceID, "url", f.URL, "err", err)
	}

	return md, nil
}

func (p processor) save(ctx context.Context, f feed.Feed) error {
	_, err := p.store.feedStore.CreateFeed(ctx, f)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("err create feed %s: %w", f.URL, err)
	}

	if err := p.store.Revive(ctx, f.SourceID); err != nil {
		return fmt.Errorf("err revive source %s: %w", f.SourceID, err)
	}

	return nil
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
