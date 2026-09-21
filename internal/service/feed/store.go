package feed

import (
	"context"
	"database/sql"
	"fmt"
	"iter"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/query"
)

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
			`SELECT id, title, content FROM feed %s ORDER BY %s %s LIMIT $1 OFFSET $2`,
			where, col, dir,
		)

		rows, err := s.db.Query(ctx, query, args...)
		if err != nil {
			yield(Feed{}, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var feed Feed

			if err := rows.Scan(&feed.ID, &feed.Title, &feed.Content); err != nil {
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

func (s *Store) GetFeed(ctx context.Context, id string) (feed Feed, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT
			id,
			title,
			content
		FROM feed
		WHERE id = $1
		`, id).Scan(&feed.ID, &feed.Title, &feed.Content); err != nil {
		return Feed{}, err
	}

	return feed, nil
}

func (s *Store) CreateFeed(ctx context.Context, feed Feed) (out Feed, err error) {
	if err = s.db.QueryRow(ctx, `
		INSERT INTO feed (title, content)
		VALUES ($1, $2)
		RETURNING
			id,
			title,
			content
		`, feed.Title, feed.Content).Scan(&out.ID, &out.Title, &out.Content); err != nil {
		return
	}

	return
}

func (s *Store) UpdateFeed(ctx context.Context, feed Feed) (out Feed, err error) {
	err = s.db.QueryRow(ctx, `
		UPDATE feed
		SET
			title = $2,
			content = $3
			updated_at = now()
		WHERE id = $1
		RETURNING
			id,
			title,
			content
		`, feed.ID, feed.Title, feed.Content).
		Scan(&out.ID, &out.Title, &out.Content)

	return
}

func (s *Store) DeleteFeed(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM feed WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return sql.ErrNoRows
	}

	return nil
}
