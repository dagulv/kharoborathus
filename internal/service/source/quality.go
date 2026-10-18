package source

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	robotadapter "github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	qualityInterval = time.Minute
	qualityBatch    = 20
)

// The mocked classifier cannot judge quality, so mocked activates every post.
func RunQuality(ctx context.Context, s *Store, tagger *robot.Tagger, mocked bool, l, calls *slog.Logger) error {
	for range schedule.Always.Tick(ctx, qualityInterval) {
		if err := checkQuality(ctx, s, tagger, mocked, l, calls); err != nil {
			l.Error("check quality", "err", err)
		}
	}

	return ctx.Err()
}

func checkQuality(ctx context.Context, s *Store, tagger *robot.Tagger, mocked bool, l, calls *slog.Logger) error {
	posts, err := s.feedStore.ListPendingQuality(ctx, qualityBatch)
	if err != nil {
		return err
	}

	counts := make(map[string]int)

	defer func() {
		if len(counts) > 0 {
			l.Info("check quality", "counts", counts)
		}
	}()

	for _, post := range posts {
		passed := true
		var err error

		if !mocked {
			passed, err = tagger.Quality(ctx, calls.With("source_id", post.SourceID, "post_id", post.ID, "url", post.URL), post.Title, post.Content)
		}

		switch {
		case errors.Is(err, robotadapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		// Jev answers 500 and network errors without ErrUnavailable. Rejecting on them would hide good posts during an outage.
		case err != nil:
			l.Warn("check quality", "post", post.ID, "err", err)
			counts["error"]++
			continue
		}

		status := feed.StatusRejected
		if passed {
			status = feed.StatusActive
		}

		if err := s.feedStore.SetStatus(ctx, post.ID, status); err != nil {
			return err
		}

		counts[status]++
	}

	return nil
}
