package source

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/iterx"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

type Service struct {
	SourceStore  *postgres.SourceStore
	DomainStore  *postgres.Store
	RawPostStore *postgres.RawPostStore
	Extractor    port.Extractor
	// Nil accepts every post that is not a duplicate, for when only the mocked classifier is available.
	Classifier port.Classifier
	// Accepted raw post ids for step 2, enrich.
	Enriched chan<- string
	Schedule schedule.Schedule
	resyncs  <-chan domain.Source
}

func NewService(store *postgres.SourceStore, domains *postgres.Store, rawPosts *postgres.RawPostStore, extractor port.Extractor, classifier port.Classifier, enriched chan<- string, schedule schedule.Schedule, resyncs <-chan domain.Source) Service {
	return Service{
		SourceStore:  store,
		DomainStore:  domains,
		RawPostStore: rawPosts,
		Extractor:    extractor,
		Classifier:   classifier,
		Enriched:     enriched,
		Schedule:     schedule,
		resyncs:      resyncs,
	}
}

func (s *Service) sourceStream(ctx context.Context, l *slog.Logger) <-chan domain.Source {
	out := make(chan domain.Source)

	go func() {
		defer close(out)

		tick := s.Schedule.Tick(ctx, schedule.TickInterval)

		paused := make(map[string]time.Time)
		var lastInterval time.Time
		sources := make([]domain.Source, 0)

		dispatch := func(source domain.Source) bool {
			select {
			case out <- source:
				paused[source.ID] = time.Now().Add(time.Duration(source.Rate) * time.Minute)
				return true
			case <-ctx.Done():
				return false
			}
		}

		getSources := func() ([]domain.Source, error) {
			sources, err := iterx.Collect(s.SourceStore.ListSources(ctx, domain.SourceFilter{
				OrderBy: "id",
				Enabled: sql.Null[bool]{V: true, Valid: true},
				Status:  sql.Null[string]{V: domain.StatusActive, Valid: true},
			}))
			if err != nil {
				return nil, fmt.Errorf("err list sources: %w", err)
			}
			return sources, nil
		}

		for {
			select {
			case source, ok := <-s.resyncs:
				if !ok {
					s.resyncs = nil
					continue
				}

				if !dispatch(source) {
					// TODO: Do not stop stream, maybe create new outside
					return
				}

			case _, ok := <-tick:
				if !ok {
					return
				}

				if time.Since(lastInterval) > schedule.RefreshInterval {
					lastInterval = time.Now()

					if newSources, err := getSources(); err != nil {
						l.Error("unable to get sources", "err", err)
					} else {
						sources = newSources
					}
				}

				for _, source := range sources {
					if pause, ok := paused[source.ID]; ok && time.Now().Before(pause) {
						continue
					}

					if !dispatch(source) {
						return
					}
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}
