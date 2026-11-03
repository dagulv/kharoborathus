package source

import (
	"context"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

// type Fetcher interface {
// 	Run(ctx context.Context, l *slog.Logger) <-chan domain.RawPost
// }

type fetched[T any] struct {
	source domain.Source
	data   T
}

func (p fetcher[T]) fetchFeed(ctx context.Context, l *slog.Logger, f fetched[T]) (domain.RawPost, error) {
	data, err := p.FetchPost(ctx, l.With("source_id", f.source.ID), f.data)
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

	if parsed.Lang == "" {
		// parsed.Lang = f.source
	}

	return parsed, nil
}
