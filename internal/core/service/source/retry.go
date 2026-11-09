package source

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

const (
	retryGap   = 5 * time.Minute
	retryBatch = 20
)

func (s *Service) retryPending(ctx context.Context, l *slog.Logger) {
	for range s.Schedule.Tick(ctx, time.Minute) {
		if err := s.retryBatch(ctx, l); err != nil && ctx.Err() == nil {
			l.Error("retry pending", "err", err)
		}
	}
}

// Stops at the first failed check, so a Jev outage costs one call a tick instead of a whole batch.
func (s *Service) retryBatch(ctx context.Context, l *slog.Logger) error {
	posts, err := s.RawPostStore.ListPending(ctx, retryBatch, retryGap)
	if err != nil {
		return err
	}

	for _, p := range posts {
		p, err := s.checkQuality(ctx, l, p)
		if errors.Is(err, pipeline.ErrSkip) {
			return nil
		}

		if err != nil {
			return err
		}

		if err := s.saveStatus(ctx, l, p); err != nil {
			return err
		}
	}

	return nil
}
