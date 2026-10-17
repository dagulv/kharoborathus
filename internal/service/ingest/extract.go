package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

const (
	scanInterval  = time.Minute
	scanBatch     = 50
	pendingWindow = 7
)

func RunExtraction(ctx context.Context, s *Store, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, scanInterval) {
		if err := extractLinks(ctx, s, l); err != nil {
			l.Error("extract links", "err", err)
		}
	}

	return ctx.Err()
}

func extractLinks(ctx context.Context, s *Store, l *slog.Logger) error {
	posts, err := s.ListUnscannedPosts(ctx, pendingWindow, scanBatch)
	if err != nil {
		return err
	}

	var queued int64

	for _, p := range posts {
		n, err := s.QueueLinks(ctx, p.ID, outboundLinks(p.Content, p.BaseURL))
		if err != nil {
			return err
		}

		queued += n
	}

	if len(posts) > 0 {
		l.Info("link extraction", "processed", len(posts), "queued", queued)
	}

	return nil
}
