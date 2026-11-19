package postgres

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

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

// A post is current when enrich wrote it after its raw post's content arrived. A resync makes it stale.
func (s *PostStore) PostCurrent(ctx context.Context, url string, contentAt time.Time) (current bool, err error) {
	if err = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE url = $1 AND updated_at >= $2)`, url, contentAt).Scan(&current); err != nil {
		return false, fmt.Errorf("err post current: %w", err)
	}

	return current, nil
}

// SavePost inserts the post, or overwrites a stale one in place, keeping its id and created_at.
// Saved is false when the post is already current, as when another worker stored it first.
func (s *PostStore) SavePost(ctx context.Context, tx pgx.Tx, p domain.Post, contentAt time.Time) (out domain.Post, saved bool, err error) {
	err = tx.QueryRow(ctx, `
		INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content, tags, posted_at, authors, simhash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], '{}'), $9, COALESCE($10::text[], '{}'), $11)
		ON CONFLICT (url) DO UPDATE SET
			source_id = EXCLUDED.source_id, lang = EXCLUDED.lang, domain_id = EXCLUDED.domain_id, title = EXCLUDED.title,
			short_content = EXCLUDED.short_content, content = EXCLUDED.content, tags = EXCLUDED.tags,
			posted_at = EXCLUDED.posted_at, authors = EXCLUDED.authors, simhash = EXCLUDED.simhash, updated_at = now()
		WHERE posts.updated_at < $12
		RETURNING id, created_at
		`, p.SourceID, p.URL, p.Lang, p.DomainID, p.Title, p.ShortContent, p.Content, p.Tags, p.PostedAt, p.Authors, p.Simhash, contentAt).Scan(&p.ID, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, nil
	}

	if err != nil {
		return p, false, fmt.Errorf("err save post %s: %w", p.URL, err)
	}

	return p, true, nil
}

func (s *PostStore) Trusted(ctx context.Context, sourceID string) (trusted bool, err error) {
	if err = s.db.QueryRow(ctx, `SELECT enabled AND status = 'active' FROM sources WHERE id = $1`, sourceID).Scan(&trusted); err != nil {
		return false, fmt.Errorf("err get source trust: %w", err)
	}

	return trusted, nil
}

const (
	invalidTextRepresentation = "22P02"
	uniqueViolation           = "23505"
)

// Postgres rejects a malformed uuid this way, which callers treat as an id that names nothing.
func isInvalidText(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == invalidTextRepresentation
}

// Only enabled, active tag keys are public. Proposed keys stay on the row for promotion to count.
const postsQuery = `
	SELECT p.id, p.source_id, p.domain_id, d.base_url, p.url, p.lang, p.title, p.short_content, %s, v.tags, ev.keys, p.authors, p.posted_at, p.created_at
	FROM posts p
	JOIN domains d ON d.id = p.domain_id
	CROSS JOIN LATERAL (
		SELECT COALESCE(array_agg(u.key ORDER BY u.ord), '{}') AS tags
		FROM unnest(p.tags) WITH ORDINALITY AS u (key, ord)
		WHERE EXISTS (SELECT 1 FROM tags t WHERE t.key = u.key AND t.enabled AND t.status = 'active')
	) v
	CROSS JOIN LATERAL (
		SELECT COALESCE(array_agg(pe.entity ORDER BY pe.entity), '{}') AS keys
		FROM post_entities pe
		JOIN entities e ON e.key = pe.entity
		WHERE pe.post_id = p.id AND e.enabled AND e.status = 'active'
	) ev`

// Lists leave the article out. Only a single post carries it.
var (
	listPostsQuery = fmt.Sprintf(postsQuery, "''")
	getPostQuery   = fmt.Sprintf(postsQuery, "p.content")
)

// p.tags is indexed and v.tags is not, so both narrow a tag filter.
func tagFilter(arg int) string {
	return fmt.Sprintf(`p.tags @> $%[1]d::text[] AND v.tags @> $%[1]d::text[]`, arg)
}

// v.tags only holds public tags, so a post counts when one of them is in the enabled category.
// p.tags && narrows through the GIN index first, since v.tags is not indexed.
func categoryFilter(arg int) string {
	return fmt.Sprintf(`p.tags && ARRAY(%[1]s) AND v.tags && ARRAY(%[1]s)`, categoryTags(arg))
}

func categoryTags(arg int) string {
	return fmt.Sprintf(`
		SELECT ct.tag FROM category_tags ct JOIN categories c ON c.key = ct.category
		WHERE c.key = $%d AND c.enabled
	`, arg)
}

func entityFilter(arg int) string {
	return fmt.Sprintf(`EXISTS (
		SELECT 1 FROM post_entities pe JOIN entities e ON e.key = pe.entity
		WHERE pe.post_id = p.id AND e.key = $%d AND e.enabled AND e.status = 'active'
	)`, arg)
}

// Newest publication first by default, with created_at for posts whose date is unknown.
const feedOrder = `coalesce(p.posted_at, p.created_at) DESC, p.id DESC`

func (s *PostStore) ListPosts(ctx context.Context, filter domain.PostFilter) iter.Seq2[domain.Post, error] {
	return func(yield func(domain.Post, error) bool) {
		args := []any{filter.GetLimit(), filter.GetOffset()}
		conds := []string{"true"}

		order := feedOrder
		if filter.OrderBy != "" || filter.Order != "" {
			col, dir := filter.GetSort()
			order = fmt.Sprintf(`p.%s %s, p.id %[2]s`, col, dir)
		}

		if filter.After != "" {
			if order != feedOrder {
				yield(domain.Post{}, fmt.Errorf("after with a custom order: %w", domain.ErrInvalidFilter))
				return
			}

			if err := s.checkCursor(ctx, filter.After); err != nil {
				yield(domain.Post{}, err)
				return
			}

			args = append(args, filter.After)
			conds = append(conds, fmt.Sprintf(`(coalesce(p.posted_at, p.created_at), p.id) < (SELECT coalesce(posted_at, created_at), id FROM posts WHERE id = $%d)`, len(args)))
		}

		if len(filter.Tags) > 0 {
			args = append(args, filter.Tags)
			conds = append(conds, tagFilter(len(args)))
		}

		for _, key := range filter.Categories {
			args = append(args, key)
			conds = append(conds, categoryFilter(len(args)))
		}

		for _, key := range filter.Entities {
			args = append(args, key)
			conds = append(conds, entityFilter(len(args)))
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

		rows, err := s.db.Query(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY %s LIMIT $1 OFFSET $2`,
			listPostsQuery, strings.Join(conds, " AND "), order), args...)
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

// A cursor that names no post would silently return an empty page.
func (s *PostStore) checkCursor(ctx context.Context, after string) error {
	var exists bool

	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE id = $1)`, after).Scan(&exists)
	if isInvalidText(err) {
		return fmt.Errorf("after %q is not a post id: %w", after, domain.ErrInvalidFilter)
	}

	if err != nil {
		return fmt.Errorf("err check cursor: %w", err)
	}

	if !exists {
		return fmt.Errorf("after %q names no post: %w", after, domain.ErrInvalidFilter)
	}

	return nil
}

// An id that is not a uuid is reported as not found.
func (s *PostStore) GetPost(ctx context.Context, id string) (domain.Post, error) {
	p, err := scanPost(s.db.QueryRow(ctx, getPostQuery+` WHERE p.id = $1`, id))
	if isInvalidText(err) {
		err = pgx.ErrNoRows
	}

	if err != nil {
		return p, fmt.Errorf("err get post %s: %w", id, err)
	}

	return p, nil
}

func scanPost(row pgx.Row) (p domain.Post, err error) {
	err = row.Scan(&p.ID, &p.SourceID, &p.DomainID, &p.DomainURL, &p.URL, &p.Lang, &p.Title, &p.ShortContent, &p.Content, &p.Tags, &p.Entities, &p.Authors, &p.PostedAt, &p.CreatedAt)

	return
}

const adminPageSize = 50

// A raw post joined to the post it became. The post's fields win, and a raw post shows what it extracted.
const (
	adminPostFrom = `
		FROM raw_posts r
		JOIN domains d ON d.id = r.domain_id
		LEFT JOIN posts p ON p.url = r.url
		LEFT JOIN sources s ON s.id = r.source_id`

	adminPostStatus = `CASE WHEN p.id IS NOT NULL AND r.status <> 'pending' THEN 'published' ELSE r.status END`

	adminPostColumns = `r.id, p.id, r.source_id, d.base_url, r.url, coalesce(p.lang, r.lang),
		coalesce(p.title, r.content->>'title', ''),
		coalesce(p.short_content, ''),
		coalesce(p.tags, '{}'),
		coalesce(p.authors, CASE WHEN jsonb_typeof(r.content->'authors') = 'array' THEN ARRAY(SELECT jsonb_array_elements_text(r.content->'authors')) END, '{}'),
		coalesce(p.posted_at, (r.content->>'posted_at')::timestamptz),
		` + adminPostStatus + `,
		r.created_at`
)

func scanAdminPost(row pgx.Row, extra ...any) (p domain.AdminPost, err error) {
	dest := []any{&p.ID, &p.PostID, &p.SourceID, &p.DomainURL, &p.URL, &p.Lang, &p.Title, &p.ShortContent, &p.Tags, &p.Authors, &p.PostedAt, &p.Status, &p.CreatedAt}
	err = row.Scan(append(dest, extra...)...)

	return
}

// Unlike ListPosts, the admin sees every raw post, and every tag on a post, proposed and disabled ones included.
func (s *PostStore) ListAdminPosts(ctx context.Context, f domain.AdminPostFilter) (domain.AdminPostPage, error) {
	page := domain.AdminPostPage{Posts: []domain.AdminPost{}, Page: f.Page, PageSize: adminPageSize}
	args := []any{}
	conds := []string{"true"}

	if f.SourceID != "" {
		args = append(args, f.SourceID)
		conds = append(conds, fmt.Sprintf(`r.source_id = $%d`, len(args)))
	}

	if f.Tag != "" {
		args = append(args, f.Tag)
		conds = append(conds, fmt.Sprintf(`p.tags @> ARRAY[$%d]::text[]`, len(args)))
	}

	if f.Category != "" {
		args = append(args, f.Category)
		conds = append(conds, fmt.Sprintf(`p.tags && ARRAY(SELECT tag FROM category_tags WHERE category = $%d)`, len(args)))
	}

	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf(adminPostStatus+` = $%d`, len(args)))
	}

	if f.Search != "" {
		args = append(args, f.Search)
		conds = append(conds, fmt.Sprintf(`(
			(
			(p.lang = 'sv' AND p.ts @@ websearch_to_tsquery('ts_swedish', $%[1]d))
			OR (p.lang = 'en' AND p.ts @@ websearch_to_tsquery('english', $%[1]d))
			OR (p.lang NOT IN ('sv', 'en') AND p.ts @@ websearch_to_tsquery('simple', $%[1]d))
			OR p.tags && ARRAY(
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
		)
			OR r.url ILIKE '%%' || $%[1]d || '%%'
			OR r.content->>'title' ILIKE '%%' || $%[1]d || '%%'
		)`, len(args)))
	}

	where := strings.Join(conds, " AND ")

	if err := s.db.QueryRow(ctx, `SELECT count(*) `+adminPostFrom+` WHERE `+where, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("err count admin posts: %w", err)
	}

	args = append(args, adminPageSize, (f.Page-1)*adminPageSize)

	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		SELECT %s %s
		WHERE %s
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $%d OFFSET $%d
		`, adminPostColumns, adminPostFrom, where, len(args)-1, len(args)), args...)
	if err != nil {
		return page, fmt.Errorf("err list admin posts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		p, err := scanAdminPost(rows)
		if err != nil {
			return page, fmt.Errorf("err scan admin post: %w", err)
		}

		page.Posts = append(page.Posts, p)
	}

	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("err read admin posts: %w", err)
	}

	return page, nil
}

// An id that is not a uuid is reported as not found.
func (s *PostStore) GetAdminPost(ctx context.Context, rawPostID string) (domain.AdminPost, error) {
	var content, sourceURL string

	p, err := scanAdminPost(s.db.QueryRow(ctx, `
		SELECT `+adminPostColumns+`, coalesce(p.content, r.content->>'content', ''), coalesce(s.url, '')
		`+adminPostFrom+`
		WHERE r.id = $1
		`, rawPostID), &content, &sourceURL)
	if isInvalidText(err) {
		err = pgx.ErrNoRows
	}

	if err != nil {
		return p, fmt.Errorf("err get admin post %s: %w", rawPostID, err)
	}

	p.Content, p.SourceURL = content, sourceURL

	return p, nil
}
