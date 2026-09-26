package discovery

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/iterx"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	tickInterval = time.Minute
	batchSize    = 20
)

func Run(ctx context.Context, s *Store, d *robot.Discoverer, sched schedule.Schedule, l *slog.Logger) error {
	for range sched.Tick(ctx, tickInterval) {
		if err := discoverPosts(ctx, s, d, l); err != nil {
			l.Error("discover posts", "err", err)
		}
	}

	return ctx.Err()
}

func discoverPosts(ctx context.Context, s *Store, d *robot.Discoverer, l *slog.Logger) error {
	posts, err := iterx.Collect(s.ListUndiscoveredPosts(ctx, batchSize))
	if err != nil {
		return err
	}

	for _, post := range posts {
		keywords, err := d.Keywords(ctx, post.Content)

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("discover keywords", "post", post.ID, "err", err)
		}

		items := make([]DiscoverItem, 0, len(keywords))

		for _, k := range keywords {
			items = append(items, DiscoverItem{PostID: post.ID, Entity: k.Entity, Action: k.Action, Query: k.Query})
		}

		if err := s.CompleteDiscovery(ctx, post.ID, items); err != nil {
			return err
		}
	}

	return nil
}
