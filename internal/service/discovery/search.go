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
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/switches"
)

const (
	searchInterval = time.Minute
	searchBatch    = 5
	dedupeWindow   = 7
	// $50 a month at $5 per 1k queries, spread over 30 days.
	dailySearchBudget = 330
)

func RunSearch(ctx context.Context, s *Store, sw *switches.Store, se Searcher, f *robot.ResultFilter, sched schedule.Schedule, l *slog.Logger) error {
	for range sched.Tick(ctx, searchInterval) {
		if err := searchEvents(ctx, s, sw, se, f, l); err != nil {
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

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("filter search results", "item", item.ID, "err", err)
		}

		n, err := s.QueueResults(ctx, item, ingest.FilterLinks(kept, item.BaseURL), len(results))
		if err != nil {
			return err
		}

		queued += n
	}

	return nil
}
