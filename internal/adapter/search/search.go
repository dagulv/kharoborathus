package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

const (
	SearchQueue  = 100
	dedupeWindow = 7
	// $50 a month at $5 per 1k queries, spread over a 31-day month.
	dailySearchBudget = 322
	// One day of ingest at its daily cap. More would age out before it is fetched.
	maxPendingResults = 200
)

type searchStore interface {
	GetQuery(ctx context.Context, id string) (SearchItem, error)
	PendingResults(ctx context.Context, windowDays int) (int, error)
	SearchesLastDay(ctx context.Context) (int, error)
	MarkRejected(ctx context.Context, id string) error
	SaveResults(ctx context.Context, id string, results []SearchResult) error
	SaveKept(ctx context.Context, item SearchItem, kept []int, urls []string) error
}

// A dropped id keeps searched_at NULL, so a catch-up job can find it.
func Enqueue(ids chan<- string, l *slog.Logger, id string) bool {
	select {
	case ids <- id:
		return true
	default:
		l.Warn("search queue full", "query", id)
		return false
	}
}

// One query at a time, so the budget and pending checks cannot race.
func RunSearch(ctx context.Context, s searchStore, ids <-chan string, se Searcher, f *ResultFilter, sched schedule.Schedule, l, calls *slog.Logger) error {
	for {
		select {
		case id := <-ids:
			if err := searchQuery(ctx, s, id, se, f, sched, l, calls); err != nil {
				l.Error("search query", "query", id, "err", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func searchQuery(ctx context.Context, s searchStore, id string, se Searcher, f *ResultFilter, sched schedule.Schedule, l, calls *slog.Logger) error {
	item, err := s.GetQuery(ctx, id)
	if err != nil || item.SearchedAt != nil {
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

	il := calls.With("discovery_search_query_id", id)

	results, err := se.Search(ctx, il, Query{Q: item.Query, Lang: item.Lang})
	if errors.Is(err, errSearchRejected) {
		l.Warn("search rejected", "query", id, "err", err)

		if err := s.MarkRejected(ctx, id); err != nil {
			return fmt.Errorf("err mark rejected: %w", err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("err search %s: %w", id, err)
	}

	// Brave billed the search, so its results are kept even when the worker is shutting down.
	if err := s.SaveResults(context.WithoutCancel(ctx), id, results); err != nil {
		return fmt.Errorf("err save results %s: %w", id, err)
	}

	if !sched.Active(time.Now()) {
		return nil
	}

	kept, err := f.Keep(ctx, il, strings.ReplaceAll(item.Entity, "_", " "), item.Action, results)
	if err != nil {
		return fmt.Errorf("err filter results %s: %w", id, err)
	}

	urls := make([]string, len(kept))
	for i, k := range kept {
		urls[i] = results[k].URL
	}

	// An empty list still marks the query as filtered.
	if kept == nil {
		kept = []int{}
	}

	if err := s.SaveKept(context.WithoutCancel(ctx), item, kept, filterLinks(urls, item.BaseURL)); err != nil {
		return fmt.Errorf("err save kept %s: %w", id, err)
	}

	l.Info("search query", "query", id, "results", len(results), "kept", len(kept))

	return nil
}
