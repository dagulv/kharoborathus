package source

import (
	"context"
	"database/sql"
	"fmt"
	"iter"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/service/feed"
)

type Store struct {
	db        *database.Database
	feedStore feed.Store
}

func NewStore(db *database.Database, feedStore feed.Store) Store {
	return Store{
		db:        db,
		feedStore: feedStore,
	}
}

func (s *Store) ListSources(ctx context.Context, filter Filter) iter.Seq2[Source, error] {
	return func(yield func(Source, error) bool) {
		args := []any{filter.GetLimit(), filter.GetOffset()}
		var where string

		col, dir := filter.GetSort()

		query := fmt.Sprintf(
			`SELECT id, adapter, url, active, rate FROM source %s ORDER BY %s %s LIMIT $1 OFFSET $2`,
			where, col, dir,
		)

		rows, err := s.db.Query(ctx, query, args...)
		if err != nil {
			yield(Source{}, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var source Source

			if err := rows.Scan(&source.ID, &source.Adapter, &source.URL, &source.Active, &source.Rate); err != nil {
				yield(Source{}, err)
				return
			}

			if !yield(source, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(Source{}, err)
		}
	}
}

func (s *Store) GetSource(ctx context.Context, id string) (source Source, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT
			id,
			adapter,
			url,
			active,
			rate,
			created_at
		FROM source
		WHERE id = $1
		`, id).Scan(&source.ID, &source.URL); err != nil {
		return Source{}, err
	}

	return source, nil
}

func (s *Store) CreateSource(ctx context.Context, source Source) (out Source, err error) {
	if err = s.db.QueryRow(ctx, `
		INSERT INTO source (adapter, url, active, rate)
		VALUES ($1, $2, $3, $4)
		RETURNING
			id,
			adapter,
			url,
			active,
			rate,
			created_at
		`, source.Adapter, source.URL, source.Active, source.Rate).Scan(&out.ID, &out.Adapter, &out.URL, &out.Active, &out.Rate, &out.CreatedAt); err != nil {
		return
	}

	return
}

func (s *Store) UpdateSource(ctx context.Context, source Source) (out Source, err error) {
	err = s.db.QueryRow(ctx, `
		UPDATE source
		SET
			adapter = $2,
			url = $3
			active = $4
			rate = $5
			updated_at = now()
		WHERE id = $1
		RETURNING
		id,
			adapter,
			url,
			active,
			rate,
			created_at
		`, source.ID, source.Adapter, source.URL, source.Active, source.Rate).
		Scan(&out.ID, &out.Adapter, &out.URL, &out.Active, &out.Rate, &out.CreatedAt)

	return
}

func (s *Store) DeleteSource(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM source WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return sql.ErrNoRows
	}

	return nil
}
