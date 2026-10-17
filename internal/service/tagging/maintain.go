package tagging

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

const (
	maintainInterval = 24 * time.Hour
	rateWindow       = 30
	dormancyWindow   = 90
)

func RunMaintenance(ctx context.Context, s *Store, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, maintainInterval) {
		rated, err := s.RecomputeRates(ctx, rateWindow, gateMaxRate, minSample)
		if err != nil {
			l.Error("recompute tag rates", "err", err)
		}

		dormant, err := s.MarkDormant(ctx, dormancyWindow)
		if err != nil {
			l.Error("mark dormant tags", "err", err)
		}

		l.Info("tag maintenance", "rated", rated, "dormant", dormant)
	}

	return ctx.Err()
}
