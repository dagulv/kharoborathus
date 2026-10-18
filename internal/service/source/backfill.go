package source

import (
	"context"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/lib/logs"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/source/adapter"
)

const backfillBatch = 10

// backfills checks every scraped source once for a WordPress REST API and sends each one found
// to the wordpress pipeline, which reads the whole archive. Posts already stored are skipped there.
func backfills(ctx context.Context, s *Store, sched schedule.Schedule, pacer *pace.Pacer, l, calls *slog.Logger) <-chan Source {
	out := make(chan Source)
	client := safehttp.NewClient(homepageTimeout)

	go func() {
		defer close(out)

		for range sched.Tick(ctx, refreshInterval) {
			sources, err := s.ListUnbackfilled(ctx, backfillBatch)
			if err != nil {
				l.Error("list unbackfilled sources", "err", err)
				continue
			}

			for _, source := range sources {
				wp, err := adapter.FindWordPress(ctx, logs.Client(client, calls.With("source_id", source.ID), "wordpress"), pacer, source.URL)

				// Marked before the backfill runs, so a failing site is not checked every minute and a
				// restart does not repeat it. Clearing backfilled_at runs it again.
				if merr := s.MarkBackfilled(ctx, source.ID); merr != nil {
					l.Error("mark backfilled", "source", source.ID, "err", merr)
					continue
				}

				if err != nil {
					l.Warn("find wordpress", "source", source.ID, "url", source.URL, "err", err)
					continue
				}

				if wp.Posts == "" {
					continue
				}

				l.Info("backfill", "source", source.ID, "url", wp.Posts, "posts", wp.Total)

				source.Adapter, source.URL, source.Lang = "wordpress", wp.Posts, wp.Lang

				select {
				case out <- source:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out
}
