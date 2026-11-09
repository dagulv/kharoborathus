package discovery

import (
	"cmp"
	"context"
	"slices"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

// New rows only, for the steps after commit.
type Found struct {
	Queries []string
	Links   []string
}

// Untrusted posts only join events a trusted post already found.
func Discover(ctx context.Context, tx pgx.Tx, post domain.Post, events []Keyword) (f Found, err error) {
	trusted, baseURL, err := postSource(ctx, tx, post)
	if err != nil {
		return Found{}, err
	}

	if trusted {
		if f.Links, err = insertLinks(ctx, tx, post.ID, outboundLinks(post.Content, baseURL)); err != nil {
			return Found{}, err
		}
	}

	quality, aliases, err := actionKeys(ctx, tx)
	if err != nil {
		return Found{}, err
	}

	type event struct{ entity, action, query string }

	var resolved []event

	for _, e := range groundKeywords(events, post.Content) {
		action, ok := resolveAction(e.Action, quality, aliases)
		if !ok {
			continue
		}

		resolved = append(resolved, event{entity: normalizedEntity(e.Entity), action: action, query: e.Query})
	}

	// Locking in key order keeps two posts with the same events from deadlocking.
	slices.SortStableFunc(resolved, func(a, b event) int {
		return cmp.Or(cmp.Compare(a.entity, b.entity), cmp.Compare(a.action, b.action))
	})

	for _, e := range resolved {
		if err := lockEvent(ctx, tx, e.entity, e.action); err != nil {
			return Found{}, err
		}

		id, found, err := recentQuery(ctx, tx, e.entity, e.action, dedupeWindow)
		if err != nil {
			return Found{}, err
		}

		if found {
			if err := linkQuery(ctx, tx, id, post.ID); err != nil {
				return Found{}, err
			}

			continue
		}

		if !trusted {
			continue
		}

		id, created, err := insertQuery(ctx, tx, e.entity, e.action, e.query)
		if err != nil {
			return Found{}, err
		}

		if err := linkQuery(ctx, tx, id, post.ID); err != nil {
			return Found{}, err
		}

		if created {
			f.Queries = append(f.Queries, id)
		}
	}

	return f, nil
}

func resolveAction(action string, quality map[string]bool, aliases map[string]string) (string, bool) {
	action = TagKey(action)
	if key, ok := aliases[action]; ok {
		action = key
	}

	if !quality[action] {
		return "", false
	}

	return action, true
}

func normalizedEntity(entity string) string {
	return TagKey(entity)
}
