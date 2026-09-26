package tagging

import (
	"context"
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
			COALESCE(array_agg(tt.term ORDER BY tt.lang, tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}')
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key
		GROUP BY t.key
		ORDER BY t.key
		`)
	if err != nil {
		return nil, err
	}

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (tag robot.Tag, err error) {
		err = row.Scan(&tag.Key, &tag.Terms)
		return
	})
}

func (s *Store) ListUntaggedPosts(ctx context.Context, limit int) iter.Seq2[Post, error] {
	return func(yield func(Post, error) bool) {
		rows, err := s.db.Query(ctx, `
			SELECT p.id, p.lang, p.title, p.content
			FROM posts p
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

			if err := rows.Scan(&post.ID, &post.Lang, &post.Title, &post.Content); err != nil {
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

	keys := make([]string, len(terms))
	langs := make([]string, len(terms))
	words := make([]string, len(terms))

	for i, t := range terms {
		keys[i], langs[i], words[i] = t.Key, t.Lang, t.Term
	}

	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tags (key)
			SELECT DISTINCT unnest($1::text[])
			ON CONFLICT (key) DO NOTHING
			`, keys); err != nil {
			return err
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO tag_terms (key, lang, term)
			SELECT * FROM unnest($1::text[], $2::text[], $3::text[])
			ON CONFLICT (key, lang, term) DO NOTHING
			`, keys, langs, words)

		return err
	})
}

func (s *Store) CompleteTagging(ctx context.Context, postID string, keys []string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if len(keys) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE posts
				SET tags = ARRAY(SELECT DISTINCT unnest(tags || $2::text[]))
				WHERE id = $1
				`, postID, keys); err != nil {
				return err
			}
		}

		_, err := tx.Exec(ctx, `INSERT INTO tag_checked (post_id) VALUES ($1) ON CONFLICT (post_id) DO NOTHING`, postID)

		return err
	})
}
