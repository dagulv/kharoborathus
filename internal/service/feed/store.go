package feed

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"iter"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/query"
	"github.com/jackc/pgx/v5"
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

func (s *Store) ListFeeds(ctx context.Context, filter query.Filter) iter.Seq2[Feed, error] {
	return func(yield func(Feed, error) bool) {
		args := []any{filter.GetLimit(), filter.GetOffset()}
		var where string

		col, dir := filter.GetSort()

		if filter.Search != "" {
			args = append(args, "%"+database.EscapeLike.Replace(filter.Search)+"%")
			where = fmt.Sprintf(`WHERE title ILIKE $%d OR content ILIKE $%d`, len(args), len(args))
		}

		query := fmt.Sprintf(
			`SELECT %s FROM posts %s ORDER BY %s %s LIMIT $1 OFFSET $2`,
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
	return scanFeed(s.db.QueryRow(ctx, `SELECT `+feedColumns+` FROM posts WHERE id = $1`, id))
}

func (s *Store) FeedExists(ctx context.Context, url string) (exists bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE url = $1)`, url).Scan(&exists)

	return
}

func (s *Store) CreateFeed(ctx context.Context, feed Feed) (Feed, error) {
	if feed.PostedAt.IsZero() {
		feed.PostedAt = time.Now()
	}

	hash := sha256.Sum256([]byte(feed.Content))

	return scanFeed(s.db.QueryRow(ctx, `
		INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, tags, authors, posted_at, content_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], '{}'), COALESCE($9::text[], '{}'), $10, $11)
		ON CONFLICT (url) DO NOTHING
		RETURNING `+feedColumns,
		feed.SourceID, feed.DomainID, feed.URL, feed.Lang, feed.Title, feed.ShortContent, feed.Content, feed.Tags, feed.Authors, feed.PostedAt, hash[:],
	))
}

func (s *Store) UpdateFeed(ctx context.Context, feed Feed) (Feed, error) {
	hash := sha256.Sum256([]byte(feed.Content))

	return scanFeed(s.db.QueryRow(ctx, `
		UPDATE posts
		SET
			title = $2,
			short_content = $3,
			content = $4,
			content_hash = $5
		WHERE id = $1
		RETURNING `+feedColumns,
		feed.ID, feed.Title, feed.ShortContent, feed.Content, hash[:],
	))
}

func (s *Store) DeleteFeed(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM posts WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return sql.ErrNoRows
	}

	return nil
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
