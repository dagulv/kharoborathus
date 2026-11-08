package discovery

import (
	"context"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

// Returns only new query ids, for step 4. Untrusted posts only join events a trusted post already found.
func Discover(ctx context.Context, tx pgx.Tx, post domain.Post, events []Keyword) ([]string, error) {
	trusted, baseURL, err := postSource(ctx, tx, post)
	if err != nil {
		return nil, err
	}

	if trusted {
		if err := insertLinks(ctx, tx, post.ID, outboundLinks(post.Content, baseURL)); err != nil {
			return nil, err
		}
	}

	quality, aliases, err := actionKeys(ctx, tx)
	if err != nil {
		return nil, err
	}

	var ids []string

	for _, e := range groundKeywords(events, post.Content) {
		action, ok := resolveAction(e.Action, quality, aliases)
		if !ok {
			continue
		}

		entity := normalizedEntity(e.Entity)

		id, found, err := recentQuery(ctx, tx, entity, action, dedupeWindow)
		if err != nil {
			return nil, err
		}

		if found {
			if err := linkQuery(ctx, tx, id, post.ID); err != nil {
				return nil, err
			}

			continue
		}

		if !trusted {
			continue
		}

		id, created, err := insertQuery(ctx, tx, entity, action, e.Query)
		if err != nil {
			return nil, err
		}

		if err := linkQuery(ctx, tx, id, post.ID); err != nil {
			return nil, err
		}

		if created {
			ids = append(ids, id)
		}
	}

	return ids, nil
}

func resolveAction(action string, quality map[string]bool, aliases map[string]string) (string, bool) {
	action = tagKey(action)
	if key, ok := aliases[action]; ok {
		action = key
	}

	if !quality[action] {
		return "", false
	}

	return action, true
}

func normalizedEntity(entity string) string {
	return tagKey(entity)
}
