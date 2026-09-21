package source

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/iterx"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/source/adapter"
)

const (
	workers         = 5
	tickInterval    = time.Second
	refreshInterval = time.Minute
)

type Pipeline interface {
	Run(ctx context.Context, l *slog.Logger) <-chan feed.Feed
}

func initPipelines(ctx context.Context, in <-chan Source, l *slog.Logger) []Pipeline {
	sinks := make(map[string]chan Source)

	pipelines := []Pipeline{
		newPipeline(adapter.NewRSS(l), addSink(sinks, "rss")),
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

func Run(ctx context.Context, s *Store, l *slog.Logger) error {
	sources := continuousSources(ctx, s, l)

	pipelines := initPipelines(ctx, sources, l)

	feeds := make([]<-chan feed.Feed, 0, len(pipelines))

	for _, p := range pipelines {
		feeds = append(feeds, p.Run(ctx, l))
	}

	for f := range merge(ctx, feeds...) {
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
					return
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

type fetched[T any] struct {
	source Source
	data   T
}

type pipeline[T any] struct {
	adapter.Adapter[T]

	in <-chan Source
}

func newPipeline[T any](a adapter.Adapter[T], in <-chan Source) Pipeline {
	return pipeline[T]{Adapter: a, in: in}
}

func (p pipeline[T]) Run(ctx context.Context, l *slog.Logger) <-chan feed.Feed {
	data := fanOut(ctx, workers, p.in, l, p.fetchFromSource)

	return fanOut(ctx, workers, data, l, p.processFeed)
}

func (p pipeline[T]) fetchFromSource(ctx context.Context, l *slog.Logger, in <-chan Source) <-chan fetched[T] {
	out := make(chan fetched[T])

	go func() {
		defer close(out)

		for source := range in {
			dataset, err := p.FetchFromSource(ctx, source.URL)
			if err != nil {
				l.Error("fetchFromSource", "source", source.ID, "url", source.URL, "err", err)
				continue
			}

			for _, data := range dataset {
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

			select {
			case out <- parsed:
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
