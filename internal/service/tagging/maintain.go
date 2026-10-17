package tagging

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/switches"
)

const (
	maintainInterval = 24 * time.Hour
	rateWindow       = 30
	dormancyWindow   = 90
	flagWindow       = 50
	flagFloor        = 0.7
)

func RunMaintenance(ctx context.Context, s *Store, sw *switches.Store, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, maintainInterval) {
		rated, err := s.RecomputeRates(ctx, rateWindow, gateMaxRate, minSample)
		if err != nil {
			l.Error("recompute tag rates", "err", err)
		}

		dormant, err := s.MarkDormant(ctx, dormancyWindow)
		if err != nil {
			l.Error("mark dormant tags", "err", err)
		}

		flagged, err := flagSources(ctx, s, sw)
		if err != nil {
			l.Error("flag sources", "err", err)
		}

		l.Info("tag maintenance", "rated", rated, "dormant", dormant, "flagged", flagged)
	}

	return ctx.Err()
}

func flagSources(ctx context.Context, s *Store, sw *switches.Store) (int64, error) {
	on, err := sw.Enabled(ctx, switches.ShadowGate)
	if err != nil || !on {
		return 0, err
	}

	return s.FlagSources(ctx, flagWindow, flagFloor)
}
