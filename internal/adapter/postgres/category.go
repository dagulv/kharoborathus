package postgres

import (
	"context"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type CategoryStore struct {
	db *database.Database
}

func NewCategoryStore(db *database.Database) CategoryStore {
	return CategoryStore{
		db: db,
	}
}

func (s *CategoryStore) EnabledCategories(ctx context.Context) ([]domain.Category, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.key, c.created_at,
			COALESCE((SELECT jsonb_object_agg(ct.lang, ct.term) FROM category_terms ct WHERE ct.key = c.key), '{}'),
			COALESCE((
				SELECT array_agg(t.key ORDER BY t.key)
				FROM category_tags cg JOIN tags t ON t.key = cg.tag
				WHERE cg.category = c.key AND t.enabled AND t.status = 'active'
			), '{}')
		FROM categories c
		WHERE c.enabled
		ORDER BY c.key
		`)
	if err != nil {
		return nil, fmt.Errorf("err query enabled categories: %w", err)
	}

	cats, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (c domain.Category, err error) {
		c.Enabled = true
		err = row.Scan(&c.Key, &c.CreatedAt, &c.Terms, &c.Tags)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan enabled categories: %w", err)
	}

	return cats, nil
}
