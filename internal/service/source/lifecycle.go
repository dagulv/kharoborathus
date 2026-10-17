package source

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/switches"
)

const (
	lifecycleInterval = 24 * time.Hour
	dormancyWindow    = 30
	homepageTimeout   = 20 * time.Second
)

func RunLifecycle(ctx context.Context, s *Store, sw *switches.Store, l *slog.Logger) error {
	client := &http.Client{Timeout: homepageTimeout}

	for range schedule.Always.Tick(ctx, lifecycleInterval) {
		if err := lifecycle(ctx, s, sw, client, l); err != nil {
			l.Error("source lifecycle", "err", err)
		}
	}

	return ctx.Err()
}

func lifecycle(ctx context.Context, s *Store, sw *switches.Store, client *http.Client, l *slog.Logger) error {
	dormant, err := s.MarkDormant(ctx, dormancyWindow)
	if err != nil {
		return err
	}

	l.Info("source lifecycle", "dormant", dormant)

	return nil
}
