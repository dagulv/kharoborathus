package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type Proposal struct {
	Key        string   `json:"key"`
	Term       string   `json:"term"`
	Categories []string `json:"categories"`
}

type TagStore struct {
	db *database.Database
}

func NewTagStore(db *database.Database) TagStore {
	return TagStore{
		db: db,
	}
}

// Active keys come first so a cap on the prompt never cuts them for proposals.
func (s *TagStore) TagKeys(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT key FROM tags ORDER BY status <> 'active', key`)
	if err != nil {
		return nil, fmt.Errorf("err query tag keys: %w", err)
	}

	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("err scan tag keys: %w", err)
	}

	return keys, nil
}

// Proposed tags are offered only while recent, so stale proposals drop out without a cleanup job.
func (s *TagStore) ClassifiableTags(ctx context.Context, lang string) ([]domain.Tag, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.key, t.status, t.type,
			COALESCE((SELECT array_agg(tt.term ORDER BY tt.term) FROM tag_terms tt WHERE tt.key = t.key AND tt.lang = $1), '{}'),
			COALESCE((SELECT array_agg(ct.category ORDER BY ct.category) FROM category_tags ct JOIN categories c ON c.key = ct.category WHERE ct.tag = t.key AND c.enabled), '{}')
		FROM tags t
		WHERE t.enabled AND (
			t.status = 'active'
			OR (t.status = 'proposed' AND (
				t.created_at > now() - interval '30 days'
				OR EXISTS (SELECT 1 FROM posts p WHERE p.tags @> ARRAY[t.key] AND p.created_at > now() - interval '30 days')
			))
		)
		ORDER BY t.key
		`, lang)
	if err != nil {
		return nil, fmt.Errorf("err query classifiable tags: %w", err)
	}

	tags, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (t domain.Tag, err error) {
		err = row.Scan(&t.Key, &t.Status, &t.Type, &t.Terms, &t.Categories)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan classifiable tags: %w", err)
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

		// RETURNING yields a row only for the creating proposal, so later proposals never touch categories.
		var created string

		err := tx.QueryRow(ctx, `INSERT INTO tags (key, status) VALUES ($1, 'proposed') ON CONFLICT (key) DO NOTHING RETURNING key`, key).Scan(&created)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("err save proposed tag %s: %w", key, err)
		}

		if created != "" && len(p.Categories) > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO category_tags (category, tag)
				SELECT c.key, $1 FROM categories c WHERE c.enabled AND c.key = ANY($2)
				ON CONFLICT DO NOTHING
				`, key, p.Categories); err != nil {
				return nil, fmt.Errorf("err save categories of proposed tag %s: %w", key, err)
			}
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

func (s *TagStore) TagOverview(ctx context.Context) (o domain.TagOverview, err error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			t.key, t.enabled, t.status, t.type, t.created_at,
			coalesce(array_agg(tt.lang || ': ' || tt.term ORDER BY tt.lang, tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}'),
			(SELECT count(*) FROM posts p WHERE p.tags @> ARRAY[t.key]),
			COALESCE((SELECT array_agg(ct.category ORDER BY ct.category) FROM category_tags ct WHERE ct.tag = t.key), '{}')
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key
		GROUP BY t.key
		ORDER BY t.key
		`)
	if err != nil {
		return o, fmt.Errorf("err list tags: %w", err)
	}

	o.Tags, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (t domain.TagSummary, err error) {
		err = row.Scan(&t.Key, &t.Enabled, &t.Status, &t.Type, &t.CreatedAt, &t.Terms, &t.PostCount, &t.Categories)
		return
	})
	if err != nil {
		return o, fmt.Errorf("err scan tags: %w", err)
	}

	// Proposed tags live in tags, and their support in posts.tags, which is what promotion counts.
	rows, err = s.db.Query(ctx, `
		SELECT
			t.key,
			coalesce(array_agg(DISTINCT tt.lang || ': ' || tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}'),
			count(DISTINCT p.id),
			count(DISTINCT p.domain_id),
			coalesce(max(p.created_at), t.created_at)
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key
		LEFT JOIN posts p ON p.tags @> ARRAY[t.key]
		WHERE t.status = 'proposed' AND t.enabled
		GROUP BY t.key
		ORDER BY count(DISTINCT p.id) DESC, t.key
		`)
	if err != nil {
		return o, fmt.Errorf("err list proposals: %w", err)
	}

	o.Proposals, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (p domain.TagProposal, err error) {
		err = row.Scan(&p.Key, &p.Terms, &p.PostCount, &p.DomainCount, &p.LastProposedAt)
		return
	})
	if err != nil {
		return o, fmt.Errorf("err scan proposals: %w", err)
	}

	rows, err = s.db.Query(ctx, `SELECT alias, key FROM tag_aliases ORDER BY alias`)
	if err != nil {
		return o, fmt.Errorf("err list aliases: %w", err)
	}

	o.Aliases, err = pgx.CollectRows(rows, pgx.RowToStructByPos[domain.TagAlias])
	if err != nil {
		return o, fmt.Errorf("err scan aliases: %w", err)
	}

	// CollectRows returns nil for no rows, and the admin reads these as JSON arrays.
	if o.Tags == nil {
		o.Tags = []domain.TagSummary{}
	}

	if o.Proposals == nil {
		o.Proposals = []domain.TagProposal{}
	}

	if o.Aliases == nil {
		o.Aliases = []domain.TagAlias{}
	}

	return o, nil
}

func (s *TagStore) SetTagEnabled(ctx context.Context, key string, enabled bool) error {
	if _, err := s.db.Exec(ctx, `UPDATE tags SET enabled = $2 WHERE key = $1`, key, enabled); err != nil {
		return fmt.Errorf("err set tag %s enabled: %w", key, err)
	}

	return nil
}

// Moves from's terms, posts, categories, entities and aliases to to, and leaves from behind as an alias of to.
func (s *TagStore) MergeTag(ctx context.Context, from, to string) error {
	if from == to {
		return domain.ErrMergeSelf
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("err begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM tags WHERE key = $1`, to).Scan(&one); err != nil {
		return fmt.Errorf("err find tag %s: %w", to, err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO tag_terms (key, lang, term)
		SELECT $2, lang, term FROM tag_terms WHERE key = $1
		ON CONFLICT DO NOTHING
		`, from, to); err != nil {
		return fmt.Errorf("err move terms: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE posts
		SET tags = ARRAY(SELECT DISTINCT unnest(array_replace(tags, $1, $2)))
		WHERE tags @> ARRAY[$1]::text[]
		`, from, to); err != nil {
		return fmt.Errorf("err move post tags: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO category_tags (category, tag)
		SELECT category, $2 FROM category_tags WHERE tag = $1
		ON CONFLICT DO NOTHING
		`, from, to); err != nil {
		return fmt.Errorf("err move category tags: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO entity_tags (entity, tag)
		SELECT entity, $2 FROM entity_tags WHERE tag = $1
		ON CONFLICT DO NOTHING
		`, from, to); err != nil {
		return fmt.Errorf("err move entity tags: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE tag_aliases SET key = $2 WHERE key = $1`, from, to); err != nil {
		return fmt.Errorf("err move aliases: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO tag_aliases (alias, key) VALUES ($1, $2)
		ON CONFLICT (alias) DO UPDATE SET key = EXCLUDED.key
		`, from, to); err != nil {
		return fmt.Errorf("err add alias: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM tags WHERE key = $1`, from); err != nil {
		return fmt.Errorf("err delete tag: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("err commit merge: %w", err)
	}

	return nil
}

// Promotes in key order, so two posts promoting the same keys lock them in the same order.
// The lock makes the count see the post a concurrent transaction committed first.
func (s *TagStore) PromoteTags(ctx context.Context, tx pgx.Tx, keys []string) ([]string, error) {
	var promoted []string

	for _, key := range slices.Compact(slices.Sorted(slices.Values(keys))) {
		var status string

		err := tx.QueryRow(ctx, `SELECT status FROM tags WHERE key = $1`, key).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("err get tag %s: %w", key, err)
		}

		if status != domain.StatusProposed {
			continue
		}

		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tag/' || $1))`, key); err != nil {
			return nil, fmt.Errorf("err lock tag %s: %w", key, err)
		}

		var done string

		err = tx.QueryRow(ctx, `
			UPDATE tags SET status = 'active'
			WHERE key = $1 AND status = 'proposed' AND EXISTS (
				SELECT 1 FROM posts p JOIN sources s ON s.id = p.source_id
				WHERE p.tags @> ARRAY[$1]::text[] AND s.enabled AND s.status = 'active' AND p.created_at > now() - interval '30 days'
				HAVING count(*) >= 5 AND count(DISTINCT p.domain_id) >= 3
			)
			RETURNING key
			`, key).Scan(&done)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("err promote tag %s: %w", key, err)
		}

		promoted = append(promoted, done)
	}

	return promoted, nil
}
