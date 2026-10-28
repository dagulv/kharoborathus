package plugin

import (
	"context"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/service/source/model"
)

type fetched[T any] struct {
	source model.Source
	data   T
}

func Log[T any](ctx context.Context, l *slog.Logger, f fetched[T]) error {
	l.Info("storing fetched")
	return nil
}

// func (p sink[T]) fetchFromSource(ctx context.Context, l *slog.Logger, in <-chan model.Source) (<-chan fetched[T], error) {
// 	out := make(chan fetched[T])
//
// 	go func() {
// 		defer close(out)
//
// 		for source := range in {
// 			dataset, err := p.FetchFromSource(ctx, p.calls.With("source_id", source.ID), source.URL)
// 			if rerr := p.store.RecordFetch(ctx, source.ID, err == nil, maxFetchFailures, minFailing); rerr != nil {
// 				l.Error("record fetch", "source", source.ID, "err", rerr)
// 			}
//
// 			if err != nil {
// 				l.Error("fetchFromSource", "source", source.ID, "url", source.URL, "err", err)
// 				continue
// 			}
//
// 			for data := range dataset {
// 				select {
// 				case out <- fetched[T]{source: source, data: data}:
// 				case <-ctx.Done():
// 					return
// 				}
// 			}
// 		}
// 	}()
//
// 	return out, nil
// }
