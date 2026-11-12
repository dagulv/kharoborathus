package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type DiscoveredStore struct {
	db *database.Database
}

func NewDiscoveredStore(db *database.Database) DiscoveredStore {
	return DiscoveredStore{
		db: db,
	}
}

type Discovered struct {
	ID         string
	URL        string
	FromPostID string
}

// Rows older than the window stay pending, so a link queued long ago is not fetched as stale news.
func (s *DiscoveredStore) GetPending(ctx context.Context, id string, windowDays int) (d Discovered, found bool, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT id, url, from_post_id FROM discovery_posts
		WHERE id = $1 AND status = 'pending' AND created_at > now() - make_interval(days => $2)
		`, id, windowDays).Scan(&d.ID, &d.URL, &d.FromPostID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Discovered{}, false, nil
	}

	if err != nil {
		return Discovered{}, false, fmt.Errorf("err get discovered post %s: %w", id, err)
	}

	return d, true, nil
}

func (s *DiscoveredStore) ListPending(ctx context.Context, windowDays, limit int) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM discovery_posts
		WHERE status = 'pending' AND created_at > now() - make_interval(days => $1)
		ORDER BY created_at DESC
		LIMIT $2
		`, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("err query pending discovered posts: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("err scan pending discovered posts: %w", err)
	}

	return ids, nil
}

func (s *DiscoveredStore) SetStatus(ctx context.Context, id, status string) error {
	if _, err := s.db.Exec(ctx, `UPDATE discovery_posts SET status = $2, checked_at = now() WHERE id = $1`, id, status); err != nil {
		return fmt.Errorf("err set discovered post status: %w", err)
	}

	return nil
}
