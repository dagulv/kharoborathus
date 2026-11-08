package postgres

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
		SELECT t.key, t.type, COALESCE(array_agg(tt.term ORDER BY tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}')
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key AND tt.lang = $1
		WHERE t.enabled AND t.status = 'active'
		GROUP BY t.key, t.type
		ORDER BY t.key
		`, lang)
	if err != nil {
		return nil, fmt.Errorf("err query active tags: %w", err)
	}

	tags, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (t domain.Tag, err error) {
		err = row.Scan(&t.Key, &t.Type, &t.Terms)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan active tags: %w", err)
	}

	return tags, nil
}

// Returns the proposed keys to store on the post. Active and disabled keys add nothing, since Jev decides active ones.
func (s *TagStore) SaveProposals(ctx context.Context, tx pgx.Tx, lang string, proposals []Proposal) ([]string, error) {
	// Saved in key order, so two posts proposing the same new keys lock them in the same order.
	sorted := slices.SortedFunc(slices.Values(proposals), func(a, b Proposal) int { return strings.Compare(a.Key, b.Key) })
	resolved := make(map[string]string, len(proposals))
	proposed := make(map[string]bool, len(proposals))

	for _, p := range sorted {
		var key string

		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT key FROM tag_aliases WHERE alias = $1), $1)`, p.Key).Scan(&key); err != nil {
			return nil, fmt.Errorf("err resolve tag alias %s: %w", p.Key, err)
		}

		if _, err := tx.Exec(ctx, `INSERT INTO tags (key, status) VALUES ($1, 'proposed') ON CONFLICT (key) DO NOTHING`, key); err != nil {
			return nil, fmt.Errorf("err save proposed tag %s: %w", key, err)
		}

		// A separate statement, so it sees a row a concurrent transaction committed while the insert waited.
		var status string
		var enabled bool

		if err := tx.QueryRow(ctx, `SELECT status, enabled FROM tags WHERE key = $1`, key).Scan(&status, &enabled); err != nil {
			return nil, fmt.Errorf("err get proposed tag %s: %w", key, err)
		}

		resolved[p.Key] = key

		if !enabled || status != domain.StatusProposed {
			continue
		}

		proposed[key] = true

		if lang == "" || p.Term == "" {
			continue
		}

		if _, err := tx.Exec(ctx, `INSERT INTO tag_terms (key, lang, term) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, key, lang, p.Term); err != nil {
			return nil, fmt.Errorf("err save proposed term %s: %w", key, err)
		}
	}

	// Returned in the model's order, most important first.
	var keys []string

	for _, p := range proposals {
		if key := resolved[p.Key]; proposed[key] && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}

	return keys, nil
}
