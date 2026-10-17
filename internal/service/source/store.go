package source

import (
	"context"
	"database/sql"
	"fmt"
	"iter"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/jackc/pgx/v5"
)

const sourceColumns = `
	id,
	domain_id,
	adapter,
	url,
	enabled,
	status,
	origin,
	rate,
	metadata,
	target_tags,
	created_at`

type Store struct {
	db          *database.Database
	feedStore   feed.Store
	domainStore domain.Store
}

func NewStore(db *database.Database, feedStore feed.Store, domainStore domain.Store) Store {
	return Store{
		db:          db,
		feedStore:   feedStore,
		domainStore: domainStore,
	}
}

func (s *Store) ListSources(ctx context.Context, filter Filter) iter.Seq2[Source, error] {
	return func(yield func(Source, error) bool) {
		args := []any{filter.GetLimit(), filter.GetOffset()}
		where := `WHERE deleted_at IS NULL`

		if filter.Enabled.Valid {
			args = append(args, filter.Enabled.V)
			where += fmt.Sprintf(` AND enabled = $%d`, len(args))
		}

		if filter.Status.Valid {
			args = append(args, filter.Status.V)
			where += fmt.Sprintf(` AND status = $%d`, len(args))
		}

		col, dir := filter.GetSort()

		query := fmt.Sprintf(
			`SELECT %s FROM sources %s ORDER BY %s %s LIMIT $1 OFFSET $2`,
			sourceColumns, where, col, dir,
		)

		rows, err := s.db.Query(ctx, query, args...)
		if err != nil {
			yield(Source{}, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			source, err := scanSource(rows)
			if err != nil {
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

func (s *Store) GetSource(ctx context.Context, id string) (Source, error) {
	return scanSource(s.db.QueryRow(ctx, `SELECT `+sourceColumns+` FROM sources WHERE id = $1 AND deleted_at IS NULL`, id))
}

func (s *Store) CreateSource(ctx context.Context, source Source) (out Source, err error) {
	if source.DomainID == "" {
		d, err := s.domainStore.GetOrCreateDomain(ctx, source.URL)
		if err != nil {
			return out, err
		}

		source.DomainID = d.ID
	}

	return scanSource(s.db.QueryRow(ctx, `
		INSERT INTO sources (domain_id, adapter, url, enabled, rate, metadata, target_tags)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6::jsonb, '{}'), COALESCE($7::text[], '{}'))
		RETURNING `+sourceColumns,
		source.DomainID, source.Adapter, source.URL, source.Enabled, source.Rate, source.Metadata, source.TargetTags,
	))
}

func (s *Store) UpdateSource(ctx context.Context, source Source) (Source, error) {
	return scanSource(s.db.QueryRow(ctx, `
		UPDATE sources
		SET
			adapter = $2,
			url = $3,
			enabled = $4,
			rate = $5,
			metadata = COALESCE($6::jsonb, '{}'),
			target_tags = COALESCE($7::text[], '{}')
		WHERE id = $1
		RETURNING `+sourceColumns,
		source.ID, source.Adapter, source.URL, source.Enabled, source.Rate, source.Metadata, source.TargetTags,
	))
}

func (s *Store) DeleteSource(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM sources WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func scanSource(row pgx.Row) (source Source, err error) {
	err = row.Scan(
		&source.ID,
		&source.DomainID,
		&source.Adapter,
		&source.URL,
		&source.Enabled,
		&source.Status,
		&source.Origin,
		&source.Rate,
		&source.Metadata,
		&source.TargetTags,
		&source.CreatedAt,
	)

	return
}

func (s *Store) RecordFetch(ctx context.Context, id string, ok bool, maxFailures int) error {
	if ok {
		if _, err := s.db.Exec(ctx, `UPDATE sources SET fetch_failures = 0 WHERE id = $1 AND fetch_failures > 0`, id); err != nil {
			return fmt.Errorf("err reset fetch failures: %w", err)
		}

		return nil
	}

	if _, err := s.db.Exec(ctx, `
		UPDATE sources
		SET
			fetch_failures = fetch_failures + 1,
			status = CASE WHEN status = 'active' AND fetch_failures + 1 >= $2 THEN 'dormant' ELSE status END,
			status_changed_at = CASE WHEN status = 'active' AND fetch_failures + 1 >= $2 THEN now() ELSE status_changed_at END
		WHERE id = $1
		`, id, maxFailures); err != nil {
		return fmt.Errorf("err record fetch failure: %w", err)
	}

	return nil
}

func (s *Store) Revive(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE sources SET status = 'active', status_changed_at = now()
		WHERE id = $1 AND status = 'dormant'
		`, id); err != nil {
		return fmt.Errorf("err revive source: %w", err)
	}

	return nil
}
