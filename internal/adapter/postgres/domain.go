package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
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
