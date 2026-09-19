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
	sources := continuousSources(ctx, s, l)
	urls := fanOut(ctx, workers, sources, l, find)
	data := fanOut(ctx, workers, urls, l, fetch)
	feeds := fanOut(ctx, workers, data, l, process)

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

func find(ctx context.Context, in <-chan Source, l *slog.Logger) <-chan found {
	out := make(chan found)

	go func() {
		defer close(out)

		for source := range in {
			a, ok := adapter.Adapters[source.Adapter]
			if !ok {
				l.Error("unknown adapter", "adapter", source.Adapter, "source", source.ID)
				continue
			}

			urls, err := a.Find(ctx, source.URL)
			if err != nil {
				l.Error("find", "source", source.ID, "err", err)
				continue
			}

			for _, url := range urls {
				select {
				case out <- found{source: source, url: url}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out
}

func fetch(ctx context.Context, in <-chan found, l *slog.Logger) <-chan fetched {
	out := make(chan fetched)

	go func() {
		defer close(out)

		for f := range in {
			a, ok := adapter.Adapters[f.source.Adapter]
			if !ok {
				l.Error("unknown adapter", "adapter", f.source.Adapter, "source", f.source.ID)
				continue
			}

			data, err := a.Fetch(ctx, f.url)
			if err != nil {
				l.Error("fetch", "source", f.source.ID, "url", f.url, "err", err)
				continue
			}

			select {
			case out <- fetched{source: f.source, url: f.url, data: data}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func process(ctx context.Context, in <-chan fetched, l *slog.Logger) <-chan feed.Feed {
	out := make(chan feed.Feed)

	go func() {
		defer close(out)

		for f := range in {
			a, ok := adapter.Adapters[f.source.Adapter]
			if !ok {
				l.Error("unknown adapter", "adapter", f.source.Adapter, "source", f.source.ID)
				continue
			}

			parsed, err := a.Process(ctx, f.data)
			if err != nil {
				l.Error("process", "source", f.source.ID, "url", f.url, "err", err)
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

func fanOut[In, Out any](ctx context.Context, n int, in <-chan In, l *slog.Logger, stage func(context.Context, <-chan In, *slog.Logger) <-chan Out) <-chan Out {
	outs := make([]<-chan Out, n)

	for i := range outs {
		outs[i] = stage(ctx, in, l)
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
