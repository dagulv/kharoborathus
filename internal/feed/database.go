package feed

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"strings"

	"github.com/jackc/pgx/v5"
)

var escapeLike = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

type database struct {
	conn *pgx.Conn
}

func NewDatabase(ctx context.Context, databaseURL string) (*database, error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, err
	}

	return &database{
		conn: conn,
	}, nil
}

func (d *database) Close(ctx context.Context) {
	d.conn.Close(ctx)
}

func (d *database) ListFeed(ctx context.Context, filter Filter) iter.Seq2[Feed, error] {
	return func(yield func(Feed, error) bool) {
		args := []any{filter.limit(), filter.offset()}
		var where string

		col, dir := filter.sort()

		if filter.Search != "" {
			args = append(args, "%"+escapeLike.Replace(filter.Search)+"%")
			where = fmt.Sprintf(`WHERE title ILIKE $%d OR content ILIKE $%d`, len(args), len(args))
		}

		query := fmt.Sprintf(
			`SELECT id, title, content FROM feed %s ORDER BY %s %s LIMIT $1 OFFSET $2`,
			where, col, dir,
		)

		rows, err := d.conn.Query(ctx, query, args...)
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

func (d *database) GetFeed(ctx context.Context, id string) (feed Feed, err error) {
	query := fmt.Sprintf(`SELECT id, title, content FROM feed WHERE id = $1`)

	if err = d.conn.QueryRow(ctx, query, id).Scan(&feed.ID, &feed.Title, &feed.Content); err != nil {
		return Feed{}, err
	}

	return feed, nil
}

func (d *database) CreateFeed(ctx context.Context, feed Feed) (Feed, error) {
	query := `INSERT INTO feed (title, content) VALUES ($1, $2) RETURNING id::text`

	if err := d.conn.QueryRow(ctx, query, feed.Title, feed.Content).Scan(&feed.ID); err != nil {
		return Feed{}, err
	}

	return feed, nil
}

func (d *database) UpdateFeed(ctx context.Context, feed Feed) (Feed, error) {
	query := fmt.Sprintf(
		`UPDATE feed SET title = $2, content = $3, updated_at = now() WHERE id = $1 RETURNING id, title, content`,
	)

	var updated Feed

	err := d.conn.QueryRow(ctx, query, feed.ID, feed.Title, feed.Content).
		Scan(&updated.ID, &updated.Title, &updated.Content)
	if err != nil {
		return Feed{}, err
	}

	return updated, nil
}

func (d *database) DeleteFeed(ctx context.Context, id string) error {
	tag, err := d.conn.Exec(ctx, `DELETE FROM feed WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return sql.ErrNoRows
	}

	return nil
}
