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
	// More would age out before discovered ingest gets to them.
	maxPendingResults = 200
	catchUpBatch      = 10
)

type searchStore interface {
	GetQuery(ctx context.Context, id string) (SearchItem, error)
	ListUnfinished(ctx context.Context, windowDays, limit int) ([]string, error)
	PendingResults(ctx context.Context, windowDays int) (int, error)
	SearchesLastDay(ctx context.Context) (int, error)
	MarkRejected(ctx context.Context, id string) error
	SaveResults(ctx context.Context, id string, results []SearchResult) error
	SaveKept(ctx context.Context, item SearchItem, kept []int, urls []string) ([]string, error)
}

// A dropped id keeps searched_at NULL, so catchUp finds it.
func Enqueue(ids chan<- string, l *slog.Logger, id string) bool {
	select {
	case ids <- id:
		return true
	default:
		l.Warn("search queue full", "query", id)
		return false
	}
}

// A dropped id stays pending, so the ingest catch-up finds it.
func EnqueueLink(ids chan<- string, l *slog.Logger, id string) {
	select {
	case ids <- id:
	default:
		l.Warn("discovered queue full", "discovery_post", id)
	}
}

// One query at a time, so the budget and pending checks cannot race.
func RunSearch(ctx context.Context, s searchStore, ids <-chan string, links chan<- string, se Searcher, f *ResultFilter, l, calls *slog.Logger) error {
	tick := schedule.Always.Tick(ctx, time.Minute)

	for {
		select {
		case id := <-ids:
			if err := searchQuery(ctx, s, id, links, se, f, l, calls); err != nil {
				l.Error("search query", "query", id, "err", err)
			}
		case <-tick:
			if err := catchUp(ctx, s, links, se, f, l, calls); err != nil && ctx.Err() == nil {
				l.Error("search catch up", "err", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Stops at the first error, so an outage costs one call a tick instead of a whole batch.
func catchUp(ctx context.Context, s searchStore, links chan<- string, se Searcher, f *ResultFilter, l, calls *slog.Logger) error {
	ids, err := s.ListUnfinished(ctx, dedupeWindow, catchUpBatch)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := searchQuery(ctx, s, id, links, se, f, l, calls); err != nil {
			return err
		}
	}

	return nil
}

func searchQuery(ctx context.Context, s searchStore, id string, links chan<- string, se Searcher, f *ResultFilter, l, calls *slog.Logger) error {
	item, err := s.GetQuery(ctx, id)
	if err != nil || item.Done {
		return err
	}

	il := calls.With("discovery_search_query_id", id)

	results := item.Results
	if item.SearchedAt == nil {
		var searched bool

		results, searched, err = search(ctx, s, item, se, l, il)
		if err != nil || !searched {
			return err
		}
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

	linkIDs, err := s.SaveKept(context.WithoutCancel(ctx), item, kept, filterLinks(urls, item.BaseURL))
	if err != nil {
		return fmt.Errorf("err save kept %s: %w", id, err)
	}

	for _, linkID := range linkIDs {
		EnqueueLink(links, l, linkID)
	}

	l.Info("search query", "query", id, "results", len(results), "kept", len(kept))

	return nil
}

// Reports false when the search was held back or rejected.
func search(ctx context.Context, s searchStore, item SearchItem, se Searcher, l, il *slog.Logger) ([]SearchResult, bool, error) {
	pending, err := s.PendingResults(ctx, dedupeWindow)
	if err != nil {
		return nil, false, err
	}

	if pending >= maxPendingResults {
		l.Info("search held back", "query", item.ID, "reason", "pending results", "pending", pending)
		return nil, false, nil
	}

	used, err := s.SearchesLastDay(ctx)
	if err != nil {
		return nil, false, err
	}

	if used >= dailySearchBudget {
		l.Info("search held back", "query", item.ID, "reason", "daily budget", "used", used)
		return nil, false, nil
	}

	results, err := se.Search(ctx, il, Query{Q: item.Query, Lang: item.Lang})
	if errors.Is(err, errSearchRejected) {
		l.Warn("search rejected", "query", item.ID, "err", err)

		if err := s.MarkRejected(ctx, item.ID); err != nil {
			return nil, false, fmt.Errorf("err mark rejected: %w", err)
		}

		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("err search %s: %w", item.ID, err)
	}

	// Brave billed the search, so its results are kept even when the worker is shutting down.
	if err := s.SaveResults(context.WithoutCancel(ctx), item.ID, results); err != nil {
		return nil, false, fmt.Errorf("err save results %s: %w", item.ID, err)
	}

	return results, true, nil
}
