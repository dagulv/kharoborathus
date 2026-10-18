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

func Run(ctx context.Context, s *Store, d *robot.Discoverer, sched schedule.Schedule, l, calls *slog.Logger) error {
	for range sched.Tick(ctx, tickInterval) {
		if err := discoverPosts(ctx, s, d, l, calls); err != nil {
			l.Error("discover posts", "err", err)
		}
	}

	return ctx.Err()
}

func discoverPosts(ctx context.Context, s *Store, d *robot.Discoverer, l, calls *slog.Logger) error {
	posts, err := iterx.Collect(s.ListUndiscoveredPosts(ctx, batchSize))
	if err != nil || len(posts) == 0 {
		return err
	}

	gateKeys, aliases, err := s.Actions(ctx)
	if err != nil {
		return err
	}

	var processed, events, failed, aiCalls int

	defer func() {
		if aiCalls > 0 {
			l.Info("discover events", "processed", processed, "events", events, "failed", failed, "aiCalls", aiCalls)
		}
	}()

	for _, post := range posts {
		keywords, err := d.Keywords(ctx, calls.With("post_id", post.ID), post.Content, gateKeys)
		aiCalls++

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("discover keywords", "post", post.ID, "err", err)
			failed++
		}

		items := eventItems(post.ID, keywords, aliases)

		if err := s.CompleteDiscovery(ctx, post.ID, items); err != nil {
			return err
		}

		processed++
		events += len(items)
	}

	return nil
}

func eventItems(postID string, keywords []robot.Keyword, aliases map[string]string) []DiscoverItem {
	items := make([]DiscoverItem, 0, len(keywords))

	for _, k := range keywords {
		action := k.Action
		if key, ok := aliases[action]; ok {
			action = key
		}

		items = append(items, DiscoverItem{PostID: postID, Entity: k.Entity, Action: action, Query: k.Query})
	}

	return items
}
