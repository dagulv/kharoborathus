package postgres

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type Proposal struct {
	Key  string `json:"key"`
	Term string `json:"term"`
}

type TagStore struct {
	db *database.Database
}

func NewTagStore(db *database.Database) TagStore {
	return TagStore{
		db: db,
	}
}

func (s *TagStore) TagKeys(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT key FROM tags ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("err query tag keys: %w", err)
	}

	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("err scan tag keys: %w", err)
	}

	return keys, nil
}

func (s *TagStore) ActiveTags(ctx context.Context, lang string) ([]domain.Tag, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.key, COALESCE(array_agg(tt.term ORDER BY tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}')
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key AND tt.lang = $1
		WHERE t.enabled AND t.status = 'active'
		GROUP BY t.key
		ORDER BY t.key
		`, lang)
	if err != nil {
		return nil, fmt.Errorf("err query active tags: %w", err)
	}

	tags, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (t domain.Tag, err error) {
		err = row.Scan(&t.Key, &t.Terms)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan active tags: %w", err)
	}

	return tags, nil
}

// Returns the proposed keys to store on the post. Active and disabled keys add nothing, since Jev decides active ones.
func (s *TagStore) SaveProposals(ctx context.Context, tx pgx.Tx, lang string, proposals []Proposal) ([]string, error) {
	var keys []string

	for _, p := range proposals {
		var key, status string
		var enabled bool

		if err := tx.QueryRow(ctx, `
			WITH k AS (SELECT COALESCE((SELECT key FROM tag_aliases WHERE alias = $1), $1) AS key),
			ins AS (INSERT INTO tags (key, status) SELECT key, 'proposed' FROM k ON CONFLICT (key) DO NOTHING RETURNING key, status, enabled)
			SELECT key, status, enabled FROM ins
			UNION ALL
			SELECT t.key, t.status, t.enabled FROM tags t, k WHERE t.key = k.key AND NOT EXISTS (SELECT 1 FROM ins)
			`, p.Key).Scan(&key, &status, &enabled); err != nil {
			return nil, fmt.Errorf("err save proposed tag %s: %w", p.Key, err)
		}

		if !enabled || status != domain.StatusProposed || slices.Contains(keys, key) {
			continue
		}

		keys = append(keys, key)

		if lang == "" || p.Term == "" {
			continue
		}

		if _, err := tx.Exec(ctx, `INSERT INTO tag_terms (key, lang, term) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, key, lang, p.Term); err != nil {
			return nil, fmt.Errorf("err save proposed term %s: %w", key, err)
		}
	}

	return keys, nil
}
