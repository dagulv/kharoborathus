package source

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	robotadapter "github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	summaryInterval = time.Minute
	summaryBatch    = 20
)

// RunSummaries summarizes active posts, a batch a minute while the LLM is open.
func RunSummaries(ctx context.Context, s *Store, summarizer *robot.Summarizer, sched schedule.Schedule, l, calls *slog.Logger) error {
	for range sched.Tick(ctx, summaryInterval) {
		if err := summarizePending(ctx, s, summarizer, l, calls); err != nil {
			l.Error("summarize pending", "err", err)
		}
	}

	return ctx.Err()
}

func summarizePending(ctx context.Context, s *Store, summarizer *robot.Summarizer, l, calls *slog.Logger) error {
	posts, err := s.feedStore.ListPendingSummaries(ctx, summaryBatch)
	if err != nil {
		return err
	}

	for _, post := range posts {
		summary, err := summarizer.Summarize(ctx, calls.With("source_id", post.SourceID, "post_id", post.ID, "url", post.URL), post.Content)

		switch {
		case errors.Is(err, robotadapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("summarize", "post", post.ID, "err", err)
		}

		if err := s.feedStore.CompleteSummary(ctx, post.ID, summary); err != nil {
			return err
		}
	}

	return nil
}
