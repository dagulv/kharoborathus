package tagging

import (
	"context"
	"fmt"
	"iter"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/service/robot"
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

func (s *Store) ListTags(ctx context.Context) ([]robot.Tag, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			t.key,
			COALESCE(array_agg(tt.term ORDER BY tt.lang, tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}'),
			t.gate
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key
		WHERE t.enabled AND t.status = 'active'
		GROUP BY t.key
		ORDER BY t.key
		`)
	if err != nil {
		return nil, err
	}

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (tag robot.Tag, err error) {
		err = row.Scan(&tag.Key, &tag.Terms, &tag.Gate)
		return
	})
}

func (s *Store) ListUntaggedPosts(ctx context.Context, limit int) iter.Seq2[Post, error] {
	return func(yield func(Post, error) bool) {
		rows, err := s.db.Query(ctx, `
			SELECT p.id, p.source_id, p.lang, p.title, p.content, s.target_tags, s.enabled AND s.status IN ('active', 'dormant')
			FROM posts p
			JOIN sources s ON s.id = p.source_id
			WHERE NOT EXISTS (SELECT 1 FROM tag_checked c WHERE c.post_id = p.id)
			ORDER BY p.created_at
			LIMIT $1
			`, limit)
		if err != nil {
			yield(Post{}, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var post Post

			if err := rows.Scan(&post.ID, &post.SourceID, &post.Lang, &post.Title, &post.Content, &post.TargetTags, &post.Trusted); err != nil {
				yield(Post{}, err)
				return
			}

			if !yield(post, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(Post{}, err)
		}
	}
}

func (s *Store) AddTags(ctx context.Context, terms []Term) error {
	if len(terms) == 0 {
		return nil
	}

	keys, langs, words := termColumns(terms)

	// A key merged away since the vocabulary snapshot must not come back, so only existing tags get terms.
	if _, err := s.db.Exec(ctx, `
		INSERT INTO tag_terms (key, lang, term)
		SELECT u.k, u.l, u.t FROM unnest($1::text[], $2::text[], $3::text[]) AS u(k, l, t)
		WHERE EXISTS (SELECT 1 FROM tags WHERE key = u.k)
		ON CONFLICT (key, lang, term) DO NOTHING
		`, keys, langs, words); err != nil {
		return fmt.Errorf("err insert tag terms: %w", err)
	}

	return nil
}

func (s *Store) CompleteTagging(ctx context.Context, postID string, keys []string, gatePassed *bool) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if len(keys) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE posts
				SET tags = ARRAY(SELECT DISTINCT unnest(tags || $2::text[]))
				WHERE id = $1
				`, postID, keys); err != nil {
				return err
			}

			if _, err := tx.Exec(ctx, `UPDATE tags SET last_matched_at = now() WHERE key = ANY($1::text[])`, keys); err != nil {
				return fmt.Errorf("err update last matched: %w", err)
			}
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO tag_checked (post_id, gate_passed) VALUES ($1, $2)
			ON CONFLICT (post_id) DO NOTHING
			`, postID, gatePassed)

		return err
	})
}

func termColumns(terms []Term) (keys, langs, words []string) {
	keys = make([]string, len(terms))
	langs = make([]string, len(terms))
	words = make([]string, len(terms))

	for i, t := range terms {
		keys[i], langs[i], words[i] = t.Key, t.Lang, t.Term
	}

	return keys, langs, words
}

func (s *Store) Vocabulary(ctx context.Context) (Vocabulary, error) {
	v := Vocabulary{Tags: make(map[string]TagState), Aliases: make(map[string]string)}

	rows, err := s.db.Query(ctx, `SELECT key, enabled, status, gate FROM tags`)
	if err != nil {
		return v, fmt.Errorf("err query tags: %w", err)
	}

	var key string
	var state TagState

	if _, err := pgx.ForEachRow(rows, []any{&key, &state.Enabled, &state.Status, &state.Gate}, func() error {
		v.Tags[key] = state
		return nil
	}); err != nil {
		return v, fmt.Errorf("err scan tags: %w", err)
	}

	rows, err = s.db.Query(ctx, `SELECT alias, key FROM tag_aliases`)
	if err != nil {
		return v, fmt.Errorf("err query tag aliases: %w", err)
	}

	var alias string

	if _, err := pgx.ForEachRow(rows, []any{&alias, &key}, func() error {
		v.Aliases[alias] = key
		return nil
	}); err != nil {
		return v, fmt.Errorf("err scan tag aliases: %w", err)
	}

	return v, nil
}

func (s *Store) AddProposals(ctx context.Context, postID, sourceID string, terms []Term) error {
	if len(terms) == 0 {
		return nil
	}

	keys, langs, words := termColumns(terms)

	if _, err := s.db.Exec(ctx, `
		INSERT INTO tag_proposals (key, lang, term, post_id, source_id)
		SELECT k, l, t, $4, $5 FROM unnest($1::text[], $2::text[], $3::text[]) AS u(k, l, t)
		ON CONFLICT (key, post_id) DO NOTHING
		`, keys, langs, words, postID, sourceID); err != nil {
		return fmt.Errorf("err insert tag proposals: %w", err)
	}

	return nil
}

func (s *Store) Reactivate(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	// Resetting last_matched_at stops the daily dormancy check from putting it straight back to sleep.
	if _, err := s.db.Exec(ctx, `
		UPDATE tags SET status = 'active', last_matched_at = now()
		WHERE key = ANY($1::text[]) AND status = 'dormant'
		`, keys); err != nil {
		return fmt.Errorf("err reactivate tags: %w", err)
	}

	return nil
}

func (s *Store) ListCandidates(ctx context.Context, windowDays, minPosts, minDomains, limit int) ([]Candidate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			p.key,
			array_agg(DISTINCT p.post_id::text),
			array_agg(p.lang ORDER BY p.post_id),
			array_agg(p.term ORDER BY p.post_id)
		FROM tag_proposals p
		JOIN posts po ON po.id = p.post_id
		JOIN sources s ON s.id = po.source_id
		WHERE p.created_at > now() - make_interval(days => $1)
			AND s.enabled
			AND s.status IN ('active', 'dormant')
			AND NOT EXISTS (SELECT 1 FROM tags t WHERE t.key = p.key)
			AND NOT EXISTS (SELECT 1 FROM tag_aliases a WHERE a.alias = p.key)
		GROUP BY p.key
		HAVING count(DISTINCT p.post_id) >= $2 AND count(DISTINCT po.domain_id) >= $3
		ORDER BY count(DISTINCT p.post_id) DESC, p.key
		LIMIT $4
		`, windowDays, minPosts, minDomains, limit)
	if err != nil {
		return nil, fmt.Errorf("err query candidates: %w", err)
	}

	candidates, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (c Candidate, err error) {
		var langs, words []string

		if err = row.Scan(&c.Key, &c.PostIDs, &langs, &words); err != nil {
			return c, err
		}

		for i := range langs {
			c.Terms = append(c.Terms, Term{Key: c.Key, Lang: langs[i], Term: words[i]})
		}

		return c, nil
	})
	if err != nil {
		return nil, fmt.Errorf("err scan candidates: %w", err)
	}

	return candidates, nil
}

func (s *Store) Similar(ctx context.Context, terms []Term) ([]robot.Tag, error) {
	_, langs, words := termColumns(terms)

	rows, err := s.db.Query(ctx, `
		SELECT t.key, array_agg(tt.term ORDER BY tt.lang, tt.term)
		FROM tags t
		JOIN tag_terms tt ON tt.key = t.key
		-- Disabled tags are included so their synonyms become aliases instead of new live tags.
		WHERE EXISTS (
				SELECT 1
				FROM tag_terms m
				JOIN unnest($1::text[], $2::text[]) AS c(lang, term) ON c.lang = m.lang
				WHERE m.key = t.key
					AND to_tsvector(
						CASE m.lang
							WHEN 'sv' THEN 'ts_swedish'::regconfig
							WHEN 'en' THEN 'english'::regconfig
							ELSE 'simple'::regconfig
						END,
						c.term
					) @@ m.matcher
			)
		GROUP BY t.key
		ORDER BY t.key
		`, langs, words)
	if err != nil {
		return nil, fmt.Errorf("err query similar tags: %w", err)
	}

	tags, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (tag robot.Tag, err error) {
		err = row.Scan(&tag.Key, &tag.Terms)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan similar tags: %w", err)
	}

	return tags, nil
}

func (s *Store) GetPosts(ctx context.Context, ids []string) ([]Post, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_id, lang, title, content
		FROM posts
		WHERE id = ANY($1::text[]::uuid[])
		`, ids)
	if err != nil {
		return nil, fmt.Errorf("err query posts: %w", err)
	}

	posts, err := pgx.CollectRows(rows, scanPost)
	if err != nil {
		return nil, fmt.Errorf("err scan posts: %w", err)
	}

	return posts, nil
}

func (s *Store) ListRecentPosts(ctx context.Context, days, limit int) ([]Post, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.source_id, p.lang, p.title, p.content
		FROM posts p
		JOIN sources s ON s.id = p.source_id
		WHERE p.created_at > now() - make_interval(days => $1)
			AND s.status IN ('active', 'dormant')
			AND s.enabled
			-- Discovered posts passed the gate, so they would skew the broadness sample.
			AND NOT EXISTS (SELECT 1 FROM post_links pl WHERE pl.from_post_id = p.id AND pl.relation = 'discovered')
		ORDER BY p.created_at DESC
		LIMIT $2
		`, days, limit)
	if err != nil {
		return nil, fmt.Errorf("err query recent posts: %w", err)
	}

	posts, err := pgx.CollectRows(rows, scanPost)
	if err != nil {
		return nil, fmt.Errorf("err scan recent posts: %w", err)
	}

	return posts, nil
}

func scanPost(row pgx.CollectableRow) (p Post, err error) {
	err = row.Scan(&p.ID, &p.SourceID, &p.Lang, &p.Title, &p.Content)
	return
}

func (s *Store) AddAlias(ctx context.Context, alias, key string, terms []Term) error {
	_, langs, words := termColumns(terms)

	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tag_terms (key, lang, term)
			SELECT $1, * FROM unnest($2::text[], $3::text[])
			ON CONFLICT (key, lang, term) DO NOTHING
			`, key, langs, words); err != nil {
			return fmt.Errorf("err insert alias terms: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO tag_aliases (alias, key) VALUES ($1, $2)
			ON CONFLICT (alias) DO UPDATE SET key = EXCLUDED.key
			`, alias, key); err != nil {
			return fmt.Errorf("err insert tag alias: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM tag_proposals WHERE key = $1`, alias); err != nil {
			return fmt.Errorf("err delete alias proposals: %w", err)
		}

		return nil
	})
}

func (s *Store) PromoteTag(ctx context.Context, p Promotion) error {
	_, langs, words := termColumns(p.Terms)

	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tags (key, gate, match_rate, last_matched_at)
			VALUES ($1, $2, $3, CASE WHEN $4::boolean THEN now() END)
			ON CONFLICT (key) DO NOTHING
			`, p.Key, p.Gate, p.MatchRate, len(p.Matched) > 0); err != nil {
			return fmt.Errorf("err insert tag: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO tag_terms (key, lang, term)
			SELECT $1, * FROM unnest($2::text[], $3::text[])
			ON CONFLICT (key, lang, term) DO NOTHING
			`, p.Key, langs, words); err != nil {
			return fmt.Errorf("err insert tag terms: %w", err)
		}

		if len(p.Matched) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE posts
				SET tags = ARRAY(SELECT DISTINCT unnest(array_append(tags, $1::text)))
				WHERE id = ANY($2::text[]::uuid[])
				`, p.Key, p.Matched); err != nil {
				return fmt.Errorf("err backfill posts: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, `DELETE FROM tag_proposals WHERE key = $1`, p.Key); err != nil {
			return fmt.Errorf("err delete proposals: %w", err)
		}

		return nil
	})
}

func (s *Store) DropProposals(ctx context.Context, key string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM tag_proposals WHERE key = $1`, key); err != nil {
		return fmt.Errorf("err drop proposals: %w", err)
	}

	return nil
}

func (s *Store) RecomputeRates(ctx context.Context, windowDays int, maxRate float64, minPosts int) (int64, error) {
	// Only posts classified after the tag existed count, otherwise new tags look rare and wrongly gate.
	// Untrusted and discovered posts all passed the gate, so counting them would make gate tags look broad.
	// Discovered posts stay excluded after their source is promoted.
	tag, err := s.db.Exec(ctx, `
		WITH rates AS (
			SELECT
				t.key,
				(count(*) FILTER (WHERE t.key = ANY (p.tags)))::real / count(*) AS rate,
				count(*) AS n
			FROM tags t
			JOIN tag_checked c ON c.checked_at >= t.created_at
			JOIN posts p ON p.id = c.post_id
			JOIN sources s ON s.id = p.source_id
			WHERE t.status = 'active'
				AND p.created_at > now() - make_interval(days => $1)
				AND s.status IN ('active', 'dormant')
				AND s.enabled
				AND NOT EXISTS (SELECT 1 FROM post_links pl WHERE pl.from_post_id = p.id AND pl.relation = 'discovered')
			GROUP BY t.key
		)
		UPDATE tags t
		SET match_rate = rates.rate, gate = rates.rate < $2 AND rates.n >= $3
		FROM rates
		WHERE rates.key = t.key
		`, windowDays, maxRate, minPosts)
	if err != nil {
		return 0, fmt.Errorf("err recompute tag rates: %w", err)
	}

	return tag.RowsAffected(), nil
}

func (s *Store) MarkDormant(ctx context.Context, days int) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE tags SET status = 'dormant'
		WHERE status = 'active' AND coalesce(last_matched_at, created_at) < now() - make_interval(days => $1)
		`, days)
	if err != nil {
		return 0, fmt.Errorf("err mark dormant tags: %w", err)
	}

	return tag.RowsAffected(), nil
}

func (s *Store) FlagSources(ctx context.Context, window int, floor float64) (int64, error) {
	// Posts checked before the last review are skipped, otherwise a kept or retargeted source is flagged again the next day.
	tag, err := s.db.Exec(ctx, `
		WITH recent AS (
			SELECT
				p.source_id,
				c.gate_passed,
				row_number() OVER (PARTITION BY p.source_id ORDER BY c.checked_at DESC) AS rank
			FROM tag_checked c
			JOIN posts p ON p.id = c.post_id
			JOIN sources s ON s.id = p.source_id
			WHERE c.gate_passed IS NOT NULL
				AND c.checked_at > coalesce(s.reviewed_at, '-infinity')
		),
		rates AS (
			SELECT source_id, avg(gate_passed::int) AS rate, count(*) AS n
			FROM recent
			WHERE rank <= $1
			GROUP BY source_id
		)
		UPDATE sources s
		SET
			flagged_at = now(),
			status = CASE WHEN s.origin = 'discovered' AND s.status IN ('active', 'dormant') THEN 'proposed' ELSE s.status END,
			status_changed_at = CASE WHEN s.origin = 'discovered' AND s.status IN ('active', 'dormant') THEN now() ELSE s.status_changed_at END
		FROM rates
		WHERE rates.source_id = s.id
			AND s.flagged_at IS NULL
			AND s.enabled
			AND s.deleted_at IS NULL
			AND rates.n = $1
			AND rates.rate < $2
		`, window, floor)
	if err != nil {
		return 0, fmt.Errorf("err flag sources: %w", err)
	}

	return tag.RowsAffected(), nil
}
