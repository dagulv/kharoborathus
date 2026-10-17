package source

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/source/adapter"
	"github.com/dagulv/kharoborathus/internal/service/switches"
)

const (
	lifecycleInterval = 24 * time.Hour
	dormancyWindow    = 30
	homepageTimeout   = 20 * time.Second

	minAccepted          = 5
	minDays              = 3
	minAcceptRate        = 0.6
	maxPromotionsPerWeek = 5
)

func RunLifecycle(ctx context.Context, s *Store, sw *switches.Store, l *slog.Logger) error {
	client := safehttp.NewClient(homepageTimeout)

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

	promoted, err := promote(ctx, s, sw, client, l)

	l.Info("source lifecycle", "dormant", dormant, "promoted", promoted)

	return err
}

func promote(ctx context.Context, s *Store, sw *switches.Store, client *http.Client, l *slog.Logger) (promoted int, err error) {
	on, err := sw.Enabled(ctx, switches.SourcePromotion)
	if err != nil || !on {
		return 0, err
	}

	done, err := s.PromotedLastWeek(ctx)
	if err != nil || done >= maxPromotionsPerWeek {
		return 0, err
	}

	// Sources whose homepage fails are skipped, so list more than the cap allows.
	candidates, err := s.ListPromotable(ctx, minAccepted, minDays, minAcceptRate, sourcePage)
	if err != nil {
		return 0, err
	}

	for _, c := range candidates {
		if done+promoted >= maxPromotionsPerWeek {
			break
		}

		page, final, err := adapter.FetchPage(ctx, client, c.URL)
		if err != nil {
			l.Warn("fetch homepage", "source", c.ID, "url", c.URL, "err", err)
			continue
		}

		name, listing := findListing(page, final, c.URL)

		if err := s.Promote(ctx, c.ID, name, listing); err != nil {
			return promoted, err
		}

		promoted++
	}

	return promoted, nil
}
