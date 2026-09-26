package domain

import (
	"context"

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

func (s *Store) GetDomain(ctx context.Context, id string) (domain Domain, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT
			id,
			base_url,
			metadata,
			created_at,
			updated_at
		FROM domains
		WHERE id = $1
		`, id).Scan(&domain.ID, &domain.BaseURL, &domain.Metadata, &domain.CreatedAt, &domain.UpdatedAt); err != nil {
		return Domain{}, err
	}

	return domain, nil
}

func (s *Store) GetOrCreateDomain(ctx context.Context, rawURL string) (domain Domain, err error) {
	baseURL, err := BaseURL(rawURL)
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
			created_at,
			updated_at
		`, baseURL).Scan(&domain.ID, &domain.BaseURL, &domain.Metadata, &domain.CreatedAt, &domain.UpdatedAt)

	return
}

func (s *Store) UpdateMetadata(ctx context.Context, id string, metadata Metadata) (domain Domain, err error) {
	err = s.db.QueryRow(ctx, `
		UPDATE domains
		SET
			metadata = $2,
			updated_at = now()
		WHERE id = $1
		RETURNING
			id,
			base_url,
			metadata,
			created_at,
			updated_at
		`, id, metadata).Scan(&domain.ID, &domain.BaseURL, &domain.Metadata, &domain.CreatedAt, &domain.UpdatedAt)

	return
}
