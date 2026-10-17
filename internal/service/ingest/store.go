package ingest

import (
	"context"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	db *database.Database
}

func NewStore(db *database.Database) Store {
	return Store{db: db}
}

func (s *Store) ListUnscannedPosts(ctx context.Context, windowDays, limit int) ([]ScanPost, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.content, d.base_url
		FROM posts p
		JOIN sources s ON s.id = p.source_id
		JOIN domains d ON d.id = p.domain_id
		WHERE s.status IN ('active', 'dormant')
			AND p.created_at > now() - make_interval(days => $1)
			AND NOT EXISTS (SELECT 1 FROM link_checked c WHERE c.post_id = p.id)
		ORDER BY p.created_at
		LIMIT $2
		`, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("err query unscanned posts: %w", err)
	}

	posts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (p ScanPost, err error) {
		err = row.Scan(&p.ID, &p.Content, &p.BaseURL)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan unscanned posts: %w", err)
	}

	return posts, nil
}

func (s *Store) QueueLinks(ctx context.Context, postID string, urls []string) (queued int64, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO link_queue (url, via, from_post_id)
			SELECT u, 'link', $2 FROM unnest($1::text[]) AS u
			WHERE NOT EXISTS (SELECT 1 FROM posts p WHERE p.url = u)
			ON CONFLICT (url) DO NOTHING
			`, urls, postID)
		if err != nil {
			return fmt.Errorf("err queue links: %w", err)
		}

		queued = tag.RowsAffected()

		if _, err := tx.Exec(ctx, `INSERT INTO link_checked (post_id) VALUES ($1) ON CONFLICT (post_id) DO NOTHING`, postID); err != nil {
			return fmt.Errorf("err mark link checked: %w", err)
		}

		return nil
	})

	return
}
