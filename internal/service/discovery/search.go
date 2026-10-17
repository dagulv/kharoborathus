package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/ingest"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/switches"
)

const (
	searchInterval = time.Minute
	searchBatch    = 5
	dedupeWindow   = 7
	// $50 a month at $5 per 1k queries, spread over 30 days.
	dailySearchBudget = 330
	// One day of ingest at its daily cap. More would age out before it is fetched.
	maxPendingResults = 200
	// Each tick buys a search, so a failing filter must not be retried every minute.
	wastedPause = 30 * time.Minute
)

var errSearchWasted = errors.New("discovery: paid search wasted")

func RunSearch(ctx context.Context, s *Store, sw *switches.Store, se Searcher, f *robot.ResultFilter, sched schedule.Schedule, l *slog.Logger) error {
	var pausedUntil time.Time

	for range sched.Tick(ctx, searchInterval) {
		if time.Now().Before(pausedUntil) {
			continue
		}

		err := searchEvents(ctx, s, sw, se, f, l)
		if errors.Is(err, errSearchWasted) {
			l.Warn("search paused", "for", wastedPause, "err", err)
			pausedUntil = time.Now().Add(wastedPause)

			continue
		}

		if err != nil {
			l.Error("search events", "err", err)
		}
	}

	return ctx.Err()
}

func searchEvents(ctx context.Context, s *Store, sw *switches.Store, se Searcher, f *robot.ResultFilter, l *slog.Logger) error {
	on, err := sw.Enabled(ctx, switches.SearchDiscovery)
	if err != nil || !on {
		return err
	}

	// Results are only worth paying for while ingest fetches them.
	on, err = sw.Enabled(ctx, switches.DiscoveredIngest)
	if err != nil || !on {
		return err
	}

	pending, err := s.PendingResults(ctx, dedupeWindow)
	if err != nil || pending >= maxPendingResults {
		return err
	}

	used, err := s.SearchesLastDay(ctx)
	if err != nil || used >= dailySearchBudget {
		return err
	}

	items, err := s.ListQueue(ctx, dedupeWindow, min(searchBatch, dailySearchBudget-used))
	if err != nil {
		return err
	}

	var searches, deduped, aiCalls int
	var queued int64

	defer func() {
		if len(items) > 0 {
			l.Info("search discovery", "processed", len(items), "searches", searches, "deduped", deduped, "queued", queued, "aiCalls", aiCalls)
		}
	}()

	for _, item := range items {
		done, err := s.Searched(ctx, item.Entity, item.Action, dedupeWindow)
		if err != nil {
			return err
		}

		if done {
			if err := s.MarkSearched(ctx, item.ID); err != nil {
				return fmt.Errorf("err mark deduped: %w", err)
			}

			deduped++

			continue
		}

		results, err := se.Search(ctx, Query{Q: item.Query, Lang: item.Lang})
		if errors.Is(err, errSearchRejected) {
			l.Warn("search rejected", "item", item.ID, "err", err)

			if err := s.MarkSearched(ctx, item.ID); err != nil {
				return fmt.Errorf("err mark rejected: %w", err)
			}

			continue
		}

		if err != nil {
			return fmt.Errorf("err search %s: %w", item.ID, err)
		}

		searches++

		if len(results) > 0 {
			aiCalls++
		}

		kept, err := f.Keep(ctx, item.Entity, item.Action, results)
		if ctx.Err() != nil {
			return nil
		}

		if err != nil {
			// The search is paid either way, so it must count against the budget.
			if _, err := s.QueueResults(ctx, item, nil, len(results)); err != nil {
				return err
			}

			return fmt.Errorf("%w: filter %s: %w", errSearchWasted, item.ID, err)
		}

		n, err := s.QueueResults(ctx, item, ingest.FilterLinks(kept, item.BaseURL), len(results))
		if err != nil {
			return err
		}

		queued += n
	}

	return nil
}
