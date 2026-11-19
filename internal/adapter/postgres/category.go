package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"

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

// Counts run on all tags, so a category shows posts carried only by proposed or disabled tags.
func (s *CategoryStore) ListAdminCategories(ctx context.Context) ([]domain.AdminCategory, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.key, c.enabled, c.created_at,
			COALESCE((SELECT jsonb_object_agg(ct.lang, ct.term) FROM category_terms ct WHERE ct.key = c.key), '{}'),
			COALESCE((SELECT array_agg(cg.tag ORDER BY cg.tag) FROM category_tags cg WHERE cg.category = c.key), '{}'),
			(SELECT count(*) FROM posts p WHERE p.tags && ARRAY(SELECT cg.tag FROM category_tags cg WHERE cg.category = c.key))
		FROM categories c
		ORDER BY c.key
		`)
	if err != nil {
		return nil, fmt.Errorf("err query admin categories: %w", err)
	}

	cats, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (c domain.AdminCategory, err error) {
		err = row.Scan(&c.Key, &c.Enabled, &c.CreatedAt, &c.Terms, &c.Tags, &c.PostCount)
		c.TagCount = len(c.Tags)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan admin categories: %w", err)
	}

	// CollectRows returns nil for no rows, and the admin reads this as a JSON array.
	if cats == nil {
		cats = []domain.AdminCategory{}
	}

	return cats, nil
}

func (s *CategoryStore) CreateCategory(ctx context.Context, c domain.NewCategory) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("err begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var created string

	err = tx.QueryRow(ctx, `INSERT INTO categories (key) VALUES ($1) ON CONFLICT (key) DO NOTHING RETURNING key`, c.Key).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrCategoryExists
	}

	if err != nil {
		return fmt.Errorf("err create category %s: %w", c.Key, err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO category_terms (key, lang, term) VALUES ($1, 'sv', $2), ($1, 'en', $3)`, c.Key, c.Sv, c.En); err != nil {
		return fmt.Errorf("err create terms of category %s: %w", c.Key, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("err commit category %s: %w", c.Key, err)
	}

	return nil
}

// Wraps pgx.ErrNoRows for an unknown key.
func (s *CategoryStore) SetCategoryEnabled(ctx context.Context, key string, enabled bool) error {
	var done string

	if err := s.db.QueryRow(ctx, `UPDATE categories SET enabled = $2 WHERE key = $1 RETURNING key`, key, enabled).Scan(&done); err != nil {
		return fmt.Errorf("err set category %s enabled: %w", key, err)
	}

	return nil
}

// Replaces all of the tag's categories. Wraps pgx.ErrNoRows for an unknown tag and
// returns domain.ErrUnknownCategory, before writing, when a key is not a category.
func (s *CategoryStore) SetTagCategories(ctx context.Context, tag string, categories []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("err begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM tags WHERE key = $1`, tag).Scan(&one); err != nil {
		return fmt.Errorf("err find tag %s: %w", tag, err)
	}

	keys := slices.Compact(slices.Sorted(slices.Values(categories)))

	var known int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM categories WHERE key = ANY($1)`, keys).Scan(&known); err != nil {
		return fmt.Errorf("err check categories: %w", err)
	}

	if known != len(keys) {
		return domain.ErrUnknownCategory
	}

	if _, err := tx.Exec(ctx, `DELETE FROM category_tags WHERE tag = $1`, tag); err != nil {
		return fmt.Errorf("err clear categories of tag %s: %w", tag, err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO category_tags (category, tag) SELECT unnest($2::text[]), $1`, tag, keys); err != nil {
		return fmt.Errorf("err set categories of tag %s: %w", tag, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("err commit categories of tag %s: %w", tag, err)
	}

	return nil
}
