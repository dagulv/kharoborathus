package source

import (
	"context"
	"errors"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

// type Fetcher interface {
// 	Run(ctx context.Context, l *slog.Logger) <-chan domain.RawPost
// }

type fetched[T any] struct {
	source domain.Source
	data   T
}

// Third-party requests go to calls, one row each in the logs table.
func NewFetcher[T any](f port.Fetcher[T], calls *slog.Logger) Fetcher {
	return fetcher[T]{Fetcher: f, calls: calls}
}

func (p fetcher[T]) Plugin(_ context.Context) pipeline.Pipeline[domain.Source, domain.RawPost] {
	return pipeline.New[domain.Source]().
		NextCh(workers, p.list("fetchFeed", p.FetchFeed)).
		Next(workers, p.fetchFeed)
}

func (p fetcher[T]) Backfill(_ context.Context) (pipeline.Pipeline[domain.Source, domain.RawPost], bool) {
	b, ok := p.Fetcher.(port.Backfiller[T])
	if !ok {
		return pipeline.Pipeline[domain.Source, domain.RawPost]{}, false
	}

	return pipeline.New[domain.Source]().
		NextCh(workers, p.list("backfill", b.Backfill)).
		Next(workers, p.fetchFeed), true
}

func (p fetcher[T]) list(name string, read func(context.Context, *slog.Logger, string) (<-chan T, error)) func(context.Context, *slog.Logger, <-chan domain.Source) <-chan fetched[T] {
	return func(ctx context.Context, l *slog.Logger, in <-chan domain.Source) <-chan fetched[T] {
		return p.listFeed(ctx, l, in, name, read)
	}
}

func (p fetcher[T]) listFeed(ctx context.Context, l *slog.Logger, in <-chan domain.Source, name string, read func(context.Context, *slog.Logger, string) (<-chan T, error)) <-chan fetched[T] {
	out := make(chan fetched[T])

	go func() {
		defer close(out)

		for source := range in {
			items, err := read(ctx, p.calls.With("source_id", source.ID), source.URL)
			if errors.Is(err, port.ErrNoArchive) {
				l.Info("no archive", "source", source.ID, "url", source.URL)
				continue
			}

			if err != nil {
				l.Error(name, "source", source.ID, "url", source.URL, "err", err)
				continue
			}

			for data := range items {
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

func (p fetcher[T]) fetchFeed(ctx context.Context, l *slog.Logger, f fetched[T]) (domain.RawPost, error) {
	data, err := p.FetchPost(ctx, p.calls.With("source_id", f.source.ID), f.data)
	if err != nil {
		l.Debug("fetchPost", "source", f.source.ID, "url", f.source.URL, "err", err)
		return domain.RawPost{}, pipeline.ErrSkip
	}

	parsed, err := p.ProcessFeed(ctx, data)
	if err != nil {
		l.Error("processFeed", "source", f.source.ID, "url", f.source.URL, "err", err)
		return domain.RawPost{}, pipeline.ErrSkip
	}

	parsed.SourceID = f.source.ID
	parsed.Lang = normalizeLang(parsed.Lang)
	if parsed.Lang == "" {
		parsed.Lang = f.source.Lang
	}

	return parsed, nil
}
