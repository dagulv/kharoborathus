package discovery

import (
	"context"
	"database/sql"
	"iter"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const queueColumns = `
	id,
	post_id,
	entity,
	action,
	query,
	searched_at,
	created_at`

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
		}

		_, err := tx.Exec(ctx, `INSERT INTO discover_checked (post_id) VALUES ($1) ON CONFLICT (post_id) DO NOTHING`, postID)

		return err
	})
}

func (s *Store) ListQueue(ctx context.Context, limit int) iter.Seq2[DiscoverItem, error] {
	return func(yield func(DiscoverItem, error) bool) {
		rows, err := s.db.Query(ctx, `
			SELECT `+queueColumns+`
			FROM discover_queue
			WHERE searched_at IS NULL
			ORDER BY created_at
			LIMIT $1
			`, limit)
		if err != nil {
			yield(DiscoverItem{}, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			item, err := scanQueueItem(rows)
			if err != nil {
				yield(DiscoverItem{}, err)
				return
			}

			if !yield(item, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(DiscoverItem{}, err)
		}
	}
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

func scanQueueItem(row pgx.Row) (item DiscoverItem, err error) {
	err = row.Scan(
		&item.ID,
		&item.PostID,
		&item.Entity,
		&item.Action,
		&item.Query,
		&item.SearchedAt,
		&item.CreatedAt,
	)

	return
}
