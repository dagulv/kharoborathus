package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	db *database.Database
}

func NewStore(db *database.Database) Store {
	return Store{
		db: db,
	}
}

func (s *Store) GetDomain(ctx context.Context, id string) (d domain.Domain, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT
			id,
			base_url,
			metadata,
			created_at
		FROM domains
		WHERE id = $1
		`, id).Scan(&d.ID, &d.BaseURL, &d.Metadata, &d.CreatedAt); err != nil {
		return domain.Domain{}, err
	}

	return d, nil
}

func (s *Store) GetOrCreateDomain(ctx context.Context, rawURL string) (d domain.Domain, err error) {
	baseURL, err := domain.BaseURL(rawURL)
	if err != nil {
		return
	}

	err = s.db.QueryRow(ctx, `
		INSERT INTO domains (base_url)
		VALUES ($1)
		ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url
		RETURNING
			id,
			base_url,
			metadata,
			created_at
		`, baseURL).Scan(&d.ID, &d.BaseURL, &d.Metadata, &d.CreatedAt)

	return
}

func (s *Store) UpdateMetadata(ctx context.Context, id string, metadata domain.Metadata) (d domain.Domain, err error) {
	err = s.db.QueryRow(ctx, `
		UPDATE domains
		SET
			metadata = $2
		WHERE id = $1
		RETURNING
			id,
			base_url,
			metadata,
			created_at
		`, id, metadata).Scan(&d.ID, &d.BaseURL, &d.Metadata, &d.CreatedAt)

	return
}

// Merges one key, so saving one field's rule never drops another's.
func (s *Store) SetExtractRule(ctx context.Context, id, key string, rule json.RawMessage) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE domains
		SET
			metadata = metadata || jsonb_build_object('extract', coalesce(metadata->'extract', '{}') || jsonb_build_object($2::text, $3::jsonb))
		WHERE id = $1
		`, id, key, string(rule)); err != nil {
		return fmt.Errorf("err set extract rule %s: %w", key, err)
	}

	return nil
}

// ListPages returns the domain's newest fetched pages.
func (s *Store) ListPages(ctx context.Context, domainID string, limit int) ([]domain.RawPost, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			url,
			raw_content
		FROM raw_posts
		WHERE domain_id = $1 AND raw_content IS NOT NULL
		ORDER BY created_at DESC
		LIMIT $2
		`, domainID, limit)
	if err != nil {
		return nil, fmt.Errorf("err list pages %s: %w", domainID, err)
	}

	pages, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (p domain.RawPost, err error) {
		err = row.Scan(&p.URL, &p.RawContent)
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("err scan pages %s: %w", domainID, err)
	}

	return pages, nil
}
