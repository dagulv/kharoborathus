package postgres

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostStore struct {
	db *database.Database
}

func NewPostStore(db *database.Database) PostStore {
	return PostStore{
		db: db,
	}
}

func (s *PostStore) PostExists(ctx context.Context, url string) (exists bool, err error) {
	if err = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE url = $1)`, url).Scan(&exists); err != nil {
		return false, fmt.Errorf("err post exists: %w", err)
	}

	return exists, nil
}

// Inserted is false when another worker stored the url first.
func (s *PostStore) InsertPost(ctx context.Context, tx pgx.Tx, p domain.Post) (out domain.Post, inserted bool, err error) {
	err = tx.QueryRow(ctx, `
		INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content, tags, posted_at, authors, simhash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], '{}'), $9, COALESCE($10::text[], '{}'), $11)
		ON CONFLICT (url) DO NOTHING
		RETURNING id, created_at
		`, p.SourceID, p.URL, p.Lang, p.DomainID, p.Title, p.ShortContent, p.Content, p.Tags, p.PostedAt, p.Authors, p.Simhash).Scan(&p.ID, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, nil
	}

	if err != nil {
		return p, false, fmt.Errorf("err insert post %s: %w", p.URL, err)
	}

	return p, true, nil
}

func (s *PostStore) Trusted(ctx context.Context, sourceID string) (trusted bool, err error) {
	if err = s.db.QueryRow(ctx, `SELECT enabled AND status = 'active' FROM sources WHERE id = $1`, sourceID).Scan(&trusted); err != nil {
		return false, fmt.Errorf("err get source trust: %w", err)
	}

	return trusted, nil
}

const invalidTextRepresentation = "22P02"

// Only enabled, active tag keys are public. Proposed keys stay on the row for promotion to count.
const listPostsQuery = `
	SELECT p.id, p.source_id, p.domain_id, p.url, p.lang, p.title, p.short_content, p.content, v.tags, p.authors, p.posted_at, p.created_at
	FROM posts p
	CROSS JOIN LATERAL (
		SELECT COALESCE(array_agg(u.key ORDER BY u.ord), '{}') AS tags
		FROM unnest(p.tags) WITH ORDINALITY AS u (key, ord)
		WHERE EXISTS (SELECT 1 FROM tags t WHERE t.key = u.key AND t.enabled AND t.status = 'active')
	) v`

func (s *PostStore) ListPosts(ctx context.Context, filter domain.PostFilter) iter.Seq2[domain.Post, error] {
	return func(yield func(domain.Post, error) bool) {
		args := []any{filter.GetLimit(), filter.GetOffset()}
		conds := []string{"true"}

		if len(filter.Tags) > 0 {
			args = append(args, filter.Tags)
			conds = append(conds, fmt.Sprintf(`v.tags @> $%d::text[]`, len(args)))
		}

		if filter.Search != "" {
			args = append(args, filter.Search)
			conds = append(conds, fmt.Sprintf(`(
				(p.lang = 'sv' AND p.ts @@ websearch_to_tsquery('ts_swedish', $%[1]d))
				OR (p.lang = 'en' AND p.ts @@ websearch_to_tsquery('english', $%[1]d))
				OR (p.lang NOT IN ('sv', 'en') AND p.ts @@ websearch_to_tsquery('simple', $%[1]d))
				OR v.tags && ARRAY(
					SELECT t.key
					FROM tag_terms t
					WHERE to_tsvector(
						CASE t.lang
							WHEN 'sv' THEN 'ts_swedish'::regconfig
							WHEN 'en' THEN 'english'::regconfig
							ELSE 'simple'::regconfig
						END,
						$%[1]d
					) @@ t.matcher
				)
			)`, len(args)))
		}

		col, dir := filter.GetSort()

		rows, err := s.db.Query(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY p.%s %s, p.id %[4]s LIMIT $1 OFFSET $2`,
			listPostsQuery, strings.Join(conds, " AND "), col, dir), args...)
		if err != nil {
			yield(domain.Post{}, fmt.Errorf("err query posts: %w", err))
			return
		}
		defer rows.Close()

		for rows.Next() {
			p, err := scanPost(rows)
			if err != nil {
				yield(domain.Post{}, fmt.Errorf("err scan post: %w", err))
				return
			}

			if !yield(p, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(domain.Post{}, fmt.Errorf("err read posts: %w", err))
		}
	}
}

// An id that is not a uuid is reported as not found.
func (s *PostStore) GetPost(ctx context.Context, id string) (domain.Post, error) {
	p, err := scanPost(s.db.QueryRow(ctx, listPostsQuery+` WHERE p.id = $1`, id))

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == invalidTextRepresentation {
		err = pgx.ErrNoRows
	}

	if err != nil {
		return p, fmt.Errorf("err get post %s: %w", id, err)
	}

	return p, nil
}

func scanPost(row pgx.Row) (p domain.Post, err error) {
	err = row.Scan(&p.ID, &p.SourceID, &p.DomainID, &p.URL, &p.Lang, &p.Title, &p.ShortContent, &p.Content, &p.Tags, &p.Authors, &p.PostedAt, &p.CreatedAt)

	return
}
