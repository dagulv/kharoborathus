package tagging

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

func Run(ctx context.Context, s *Store, t *robot.Tagger, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, tickInterval) {
		if err := tagPosts(ctx, s, t, l); err != nil {
			l.Error("tag posts", "err", err)
		}
	}

	return ctx.Err()
}

func tagPosts(ctx context.Context, s *Store, t *robot.Tagger, l *slog.Logger) error {
	tags, err := s.ListTags(ctx)
	if err != nil || len(tags) == 0 {
		return err
	}

	posts, err := iterx.Collect(s.ListUntaggedPosts(ctx, batchSize))
	if err != nil {
		return err
	}

	for _, post := range posts {
		keys, err := t.Tags(ctx, post.Title, post.Content, tags)

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("classify tags", "post", post.ID, "err", err)
		}

		if err := s.CompleteTagging(ctx, post.ID, keys); err != nil {
			return err
		}
	}

	return nil
}
