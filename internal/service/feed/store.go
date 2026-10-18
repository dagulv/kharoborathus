package feed

import (
	"context"
	"crypto/sha256"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const (
	duplicateWindowDays  = 7
	maxDuplicateDistance = 3
)

const feedColumns = `
	id,
	source_id,
	domain_id,
	url,
	lang,
	title,
	short_content,
	content,
	tags,
	authors,
	posted_at,
	created_at`

type Store struct {
	db *database.Database
}

func NewStore(db *database.Database) Store {
	return Store{
		db: db,
	}
}

func (s *Store) ListPosts(ctx context.Context, filter Filter) iter.Seq2[Feed, error] {
	return func(yield func(Feed, error) bool) {
		args := []any{filter.GetLimit(), filter.GetOffset()}
		conds := []string{`p.status = 'active'`}

		col, dir := filter.GetSort()

		if len(filter.Tags) > 0 {
			args = append(args, filter.Tags)
			conds = append(conds, fmt.Sprintf(`p.tags @> $%d::text[]`, len(args)))
		}

		if filter.Search != "" {
			args = append(args, filter.Search)
			conds = append(conds, fmt.Sprintf(`(
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
			)`, len(args)))
		}

		where := "WHERE " + strings.Join(conds, " AND ")

		query := fmt.Sprintf(
			`SELECT %s FROM posts p %s ORDER BY p.%s %s LIMIT $1 OFFSET $2`,
			feedColumns, where, col, dir,
		)

		rows, err := s.db.Query(ctx, query, args...)
		if err != nil {
			yield(Feed{}, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			feed, err := scanFeed(rows)
			if err != nil {
				yield(Feed{}, err)
				return
			}

			if !yield(feed, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(Feed{}, err)
		}
	}
}

func (s *Store) GetFeed(ctx context.Context, id string) (Feed, error) {
	return scanFeed(s.db.QueryRow(ctx, `SELECT `+feedColumns+` FROM posts WHERE id = $1 AND status = 'active'`, id))
}

func (s *Store) FeedExists(ctx context.Context, url string) (exists bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE url = $1)`, url).Scan(&exists)

	return
}

func (s *Store) CreateFeed(ctx context.Context, feed Feed) (out Feed, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		out, err = InsertFeed(ctx, tx, feed)
		return err
	})

	return
}

func InsertFeed(ctx context.Context, tx pgx.Tx, feed Feed) (out Feed, err error) {
	if feed.PostedAt.IsZero() {
		feed.PostedAt = time.Now()
	}

	status := StatusPending
	if feed.Status != "" {
		status = feed.Status
	}

	hash := sha256.Sum256([]byte(feed.Content))

	out, err = scanFeed(tx.QueryRow(ctx, `
		INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, tags, authors, posted_at, content_hash, simhash, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], '{}'), COALESCE($9::text[], '{}'), $10, $11, $12, $13)
		ON CONFLICT (url) DO NOTHING
		RETURNING `+feedColumns,
		feed.SourceID, feed.DomainID, feed.URL, feed.Lang, feed.Title, feed.ShortContent, feed.Content, feed.Tags, feed.Authors, feed.PostedAt, hash[:], textSimhash(feed.Content), status,
	))
	if err != nil {
		return out, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO post_links (from_post_id, to_post_id, relation, metadata)
		SELECT n.id, o.id, 'duplicate_of', jsonb_build_object('distance', o.distance)
		FROM posts n
		CROSS JOIN LATERAL (
			SELECT p.id, bit_count((p.simhash # n.simhash)::bit(64)) AS distance
			FROM posts p
			WHERE p.created_at < n.created_at
				AND p.created_at > n.created_at - $2 * interval '1 day'
				AND bit_count((p.simhash # n.simhash)::bit(64)) <= $3
			ORDER BY p.created_at
			LIMIT 1
		) o
		WHERE n.id = $1
		`, out.ID, duplicateWindowDays, maxDuplicateDistance); err != nil {
		return out, fmt.Errorf("err link duplicate: %w", err)
	}

	return out, nil
}

func (s *Store) HasDuplicate(ctx context.Context, content string) (bool, error) {
	hash := textSimhash(content)
	if hash == nil {
		return false, nil
	}

	var exists bool

	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM posts
			WHERE created_at > now() - $2 * interval '1 day'
				AND bit_count((simhash # $1::bigint)::bit(64)) <= $3
		)
		`, *hash, duplicateWindowDays, maxDuplicateDistance).Scan(&exists); err != nil {
		return false, fmt.Errorf("err check duplicate: %w", err)
	}

	return exists, nil
}

func (s *Store) ListPendingSummaries(ctx context.Context, limit int) ([]Feed, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+feedColumns+`
		FROM posts p
		WHERE p.status = 'active'
			AND NOT EXISTS (SELECT 1 FROM summary_checked c WHERE c.post_id = p.id)
		ORDER BY p.created_at
		LIMIT $1
		`, limit)
	if err != nil {
		return nil, fmt.Errorf("err query pending summaries: %w", err)
	}

	feeds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Feed, error) {
		return scanFeed(row)
	})
	if err != nil {
		return nil, fmt.Errorf("err scan pending summaries: %w", err)
	}

	return feeds, nil
}

// An empty summary keeps the excerpt, so a post the summarizer cannot handle still leaves the queue.
func (s *Store) CompleteSummary(ctx context.Context, id, summary string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE posts SET short_content = coalesce(nullif($2, ''), short_content) WHERE id = $1`, id, summary); err != nil {
			return fmt.Errorf("err complete summary: %w", err)
		}

		if _, err := tx.Exec(ctx, `INSERT INTO summary_checked (post_id) VALUES ($1) ON CONFLICT (post_id) DO NOTHING`, id); err != nil {
			return fmt.Errorf("err mark summarized: %w", err)
		}

		return nil
	})
}

// Stored as the uint64 bit pattern, so half of all hashes are negative. Only bits are compared.
func textSimhash(content string) *int64 {
	h, ok := simhash(content)
	if !ok {
		return nil
	}

	v := int64(h)

	return &v
}

func scanFeed(row pgx.Row) (feed Feed, err error) {
	err = row.Scan(
		&feed.ID,
		&feed.SourceID,
		&feed.DomainID,
		&feed.URL,
		&feed.Lang,
		&feed.Title,
		&feed.ShortContent,
		&feed.Content,
		&feed.Tags,
		&feed.Authors,
		&feed.PostedAt,
		&feed.CreatedAt,
	)

	return
}

func (s *Store) ListPendingQuality(ctx context.Context, limit int) ([]Feed, error) {
	rows, err := s.db.Query(ctx, `SELECT `+feedColumns+` FROM posts WHERE status = 'pending' ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("err query pending quality: %w", err)
	}

	feeds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Feed, error) {
		return scanFeed(row)
	})
	if err != nil {
		return nil, fmt.Errorf("err scan pending quality: %w", err)
	}

	return feeds, nil
}

func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	if _, err := s.db.Exec(ctx, `UPDATE posts SET status = $2 WHERE id = $1`, id, status); err != nil {
		return fmt.Errorf("err set post status: %w", err)
	}

	return nil
}
