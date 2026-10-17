package discovery

import (
	"context"
	"database/sql"
	"fmt"
	"iter"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const eventWindowDays = 7

type Store struct {
	db *database.Database
}

func NewStore(db *database.Database) Store {
	return Store{
		db: db,
	}
}

func (s *Store) ListUndiscoveredPosts(ctx context.Context, limit int) iter.Seq2[Post, error] {
	return func(yield func(Post, error) bool) {
		rows, err := s.db.Query(ctx, `
			SELECT p.id, p.content
			FROM posts p
			WHERE NOT EXISTS (SELECT 1 FROM discover_checked c WHERE c.post_id = p.id)
			ORDER BY p.created_at
			LIMIT $1
			`, limit)
		if err != nil {
			yield(Post{}, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var post Post

			if err := rows.Scan(&post.ID, &post.Content); err != nil {
				yield(Post{}, err)
				return
			}

			if !yield(post, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(Post{}, err)
		}
	}
}

func (s *Store) CompleteDiscovery(ctx context.Context, postID string, items []DiscoverItem) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if len(items) > 0 {
			entities := make([]string, len(items))
			actions := make([]string, len(items))
			queries := make([]string, len(items))

			for i, item := range items {
				entities[i], actions[i], queries[i] = item.Entity, item.Action, item.Query
			}

			if _, err := tx.Exec(ctx, `
				INSERT INTO discover_queue (post_id, entity, action, query)
				SELECT $1, * FROM unnest($2::text[], $3::text[], $4::text[])
				ON CONFLICT (post_id, query) DO NOTHING
				`, postID, entities, actions, queries); err != nil {
				return err
			}

			if _, err := tx.Exec(ctx, `
				INSERT INTO post_links (from_post_id, to_post_id, relation, metadata)
				SELECT n.id, anchor.id, 'same_event', jsonb_build_object('entity', e.entity, 'action', e.action)
				FROM posts n
				JOIN discover_queue e ON e.post_id = n.id
				CROSS JOIN LATERAL (
					SELECT o.id
					FROM discover_queue q
					JOIN posts o ON o.id = q.post_id
					WHERE lower(q.entity) = lower(e.entity)
						AND lower(q.action) = lower(e.action)
						AND o.created_at < n.created_at
						AND o.created_at > n.created_at - $2 * interval '1 day'
					ORDER BY o.created_at
					LIMIT 1
				) anchor
				WHERE n.id = $1
				ON CONFLICT (from_post_id, to_post_id, relation) DO NOTHING
				`, postID, eventWindowDays); err != nil {
				return fmt.Errorf("err link same event: %w", err)
			}
		}

		_, err := tx.Exec(ctx, `INSERT INTO discover_checked (post_id) VALUES ($1) ON CONFLICT (post_id) DO NOTHING`, postID)

		return err
	})
}

func (s *Store) Actions(ctx context.Context) (gateKeys []string, aliases map[string]string, err error) {
	rows, err := s.db.Query(ctx, `SELECT key FROM tags WHERE enabled AND status = 'active' AND gate ORDER BY key`)
	if err != nil {
		return nil, nil, fmt.Errorf("err query gate tags: %w", err)
	}

	gateKeys, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, nil, fmt.Errorf("err scan gate tags: %w", err)
	}

	rows, err = s.db.Query(ctx, `SELECT alias, key FROM tag_aliases`)
	if err != nil {
		return nil, nil, fmt.Errorf("err query tag aliases: %w", err)
	}

	aliases = make(map[string]string)

	var alias, key string

	if _, err = pgx.ForEachRow(rows, []any{&alias, &key}, func() error {
		aliases[alias] = key
		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("err scan tag aliases: %w", err)
	}

	return gateKeys, aliases, nil
}

func (s *Store) ListQueue(ctx context.Context, windowDays, limit int) ([]SearchItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT q.id, q.post_id, q.entity, q.action, q.query, p.lang, d.base_url
		FROM discover_queue q
		JOIN posts p ON p.id = q.post_id
		JOIN sources s ON s.id = p.source_id
		JOIN domains d ON d.id = p.domain_id
		JOIN tags t ON t.key = q.action AND t.enabled AND t.status = 'active' AND t.gate
		WHERE q.searched_at IS NULL
			AND s.status IN ('active', 'dormant')
			AND s.enabled
			AND q.created_at > now() - make_interval(days => $1)
		ORDER BY q.created_at DESC
		LIMIT $2
		`, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("err query search queue: %w", err)
	}

	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (i SearchItem, err error) {
		err = row.Scan(&i.ID, &i.PostID, &i.Entity, &i.Action, &i.Query, &i.Lang, &i.BaseURL)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan search queue: %w", err)
	}

	return items, nil
}

func (s *Store) Searched(ctx context.Context, entity, action string, windowDays int) (found bool, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM discover_queue
			WHERE lower(entity) = lower($1)
				AND lower(action) = lower($2)
				AND searched_at > now() - make_interval(days => $3)
		)
		`, entity, action, windowDays).Scan(&found); err != nil {
		return false, fmt.Errorf("err check searched event: %w", err)
	}

	return found, nil
}

func (s *Store) SearchesLastDay(ctx context.Context) (n int, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT count(*) FROM discover_queue
		WHERE searched_at > now() - interval '1 day' AND result_count IS NOT NULL
		`).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count searches: %w", err)
	}

	return n, nil
}

func (s *Store) PendingResults(ctx context.Context, windowDays int) (n int, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT count(*) FROM link_queue
		WHERE via = 'search' AND status = 'pending' AND created_at > now() - make_interval(days => $1)
		`, windowDays).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count pending search results: %w", err)
	}

	return n, nil
}

func (s *Store) QueueResults(ctx context.Context, item SearchItem, urls []string, resultCount int) (queued int64, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO link_queue (url, via, from_post_id, discover_item_id)
			SELECT u, 'search', $2, $3 FROM unnest($1::text[]) AS u
			WHERE NOT EXISTS (SELECT 1 FROM posts p WHERE p.url = u)
			ON CONFLICT (url) DO NOTHING
			`, urls, item.PostID, item.ID)
		if err != nil {
			return fmt.Errorf("err queue search results: %w", err)
		}

		queued = tag.RowsAffected()

		if _, err := tx.Exec(ctx, `UPDATE discover_queue SET searched_at = now(), result_count = $2 WHERE id = $1`, item.ID, resultCount); err != nil {
			return fmt.Errorf("err mark searched: %w", err)
		}

		return nil
	})

	return
}

func (s *Store) MarkSearched(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `UPDATE discover_queue SET searched_at = now() WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return sql.ErrNoRows
	}

	return nil
}
