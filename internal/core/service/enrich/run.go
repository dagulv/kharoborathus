package enrich

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	Queue   = 100
	workers = 5
	// The job only takes raw posts older than this, so it stays off posts the live path is handling.
	catchUpGap   = 5 * time.Minute
	catchUpBatch = 20
)

// A dropped id is found again by the catch-up job.
func Enqueue(ids chan<- string, l *slog.Logger, id string) bool {
	select {
	case ids <- id:
		return true
	default:
		l.Warn("enrich queue full", "raw_post", id)
		return false
	}
}

func (s *Service) Run(ctx context.Context, ids <-chan string, l *slog.Logger) error {
	g, ctx := errgroup.WithContext(ctx)

	for range workers {
		g.Go(func() error {
			for {
				select {
				case id := <-ids:
					if err := s.Enrich(ctx, l, id); err != nil && ctx.Err() == nil {
						l.Error("enrich", "raw_post", id, "err", err)
					}
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		})
	}

	g.Go(func() error {
		for range s.LLM.Schedule().Tick(ctx, time.Minute) {
			if err := s.catchUp(ctx, l); err != nil && ctx.Err() == nil {
				l.Error("enrich catch up", "err", err)
			}
		}

		return ctx.Err()
	})

	return g.Wait()
}

// Stops at the first error, so an outage costs one call a tick instead of a whole batch.
func (s *Service) catchUp(ctx context.Context, l *slog.Logger) error {
	ids, err := s.RawPosts.ListUnenriched(ctx, catchUpBatch, catchUpGap, maxRetries)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := s.Enrich(ctx, l, id); err != nil {
			return err
		}
	}

	return nil
}
