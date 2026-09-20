package source

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/dagulv/palantir/internal/lib/iterx"
	"github.com/dagulv/palantir/internal/service/feed"
	"github.com/dagulv/palantir/internal/service/source/adapter"
)

const (
	workers         = 5
	tickInterval    = time.Second
	refreshInterval = time.Minute
)

type found struct {
	source Source
	url    string
}

type fetched struct {
	source Source
	url    string
	data   []byte
}

func Run(ctx context.Context, s *Store, l *slog.Logger) error {
	adapters := adapter.InitAdapters(l)

	sources := continuousSources(ctx, s, l)
	data := fanOut(ctx, adapters, workers, sources, l, fetchFromSource)
	feeds := fanOut(ctx, adapters, workers, data, l, processFeed)

	for f := range feeds {
		if _, err := s.feedStore.CreateFeed(ctx, f); err != nil {
			l.Error("create feed", "source", f.SourceID, "url", f.URL, "err", err)
		}
	}

	return ctx.Err()
}

func continuousSources(ctx context.Context, s *Store, l *slog.Logger) <-chan Source {
	out := make(chan Source)

	go func() {
		defer close(out)

		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()

		paused := make(map[string]time.Time)
		lastInterval := time.Now()
		sources := make([]Source, 0)
		var err error

		for {
			if time.Since(lastInterval) > refreshInterval {
				sources, err = iterx.Collect(s.ListSources(ctx, Filter{Active: sql.Null[bool]{V: true}}))
				if err != nil {
					l.Error("unable to get sources", "err", err)
					continue
				}

				lastInterval = time.Now()
			}

			for _, source := range sources {
				if pause, ok := paused[source.ID]; ok && time.Now().Before(pause) {
					continue
				}

				select {
				case out <- source:
					paused[source.ID] = time.Now().Add(time.Duration(source.Rate) * time.Minute)
				case <-ctx.Done():
					return
				}
			}

			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func fetchFromSource(ctx context.Context, adapters map[string]adapter.Adapter, in <-chan Source, l *slog.Logger) <-chan fetched {
	out := make(chan fetched)

	go func() {
		defer close(out)

		for source := range in {
			a, ok := adapters[source.Adapter]
			if !ok {
				l.Error("unknown adapter", "adapter", source.Adapter, "source", source.ID)
				continue
			}

			dataset, err := a.FetchFromSource(ctx, source.URL)
			if err != nil {
				l.Error("fetchFromSource", "source", source.ID, "url", source.URL, "err", err)
				continue
			}

			for _, data := range dataset {
				select {
				case out <- fetched{source: source, url: source.URL, data: data}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out
}

func processFeed(ctx context.Context, adapters map[string]adapter.Adapter, in <-chan fetched, l *slog.Logger) <-chan feed.Feed {
	out := make(chan feed.Feed)

	go func() {
		defer close(out)

		for f := range in {
			a, ok := adapters[f.source.Adapter]
			if !ok {
				l.Error("unknown adapter", "adapter", f.source.Adapter, "source", f.source.ID)
				continue
			}

			parsed, err := a.ProcessFeed(ctx, f.data)
			if err != nil {
				l.Error("processFeed", "source", f.source.ID, "url", f.url, "err", err)
				continue
			}

			parsed.SourceID = f.source.ID
			parsed.URL = f.url

			select {
			case out <- parsed:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func fanOut[In, Out any](ctx context.Context, adapters map[string]adapter.Adapter, n int, in <-chan In, l *slog.Logger, stage func(context.Context, map[string]adapter.Adapter, <-chan In, *slog.Logger) <-chan Out) <-chan Out {
	outs := make([]<-chan Out, n)

	for i := range outs {
		outs[i] = stage(ctx, adapters, in, l)
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
