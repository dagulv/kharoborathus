package postgres

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const sourceColumns = `
	id,
	domain_id,
	adapter,
	url,
	enabled,
	status,
	rate,
	lang,
	metadata,
	created_at`

type SourceStore struct {
	db *database.Database
}

func NewSourceStore(db *database.Database) SourceStore {
	return SourceStore{
		db: db,
	}
}

func (s *SourceStore) ListSources(ctx context.Context, filter domain.SourceFilter) iter.Seq2[domain.Source, error] {
	return func(yield func(domain.Source, error) bool) {
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

		if filter.DomainID.Valid {
			args = append(args, filter.DomainID.V)
			where += fmt.Sprintf(` AND domain_id = $%d`, len(args))
		}

		if filter.Adapter.Valid {
			args = append(args, filter.Adapter.V)
			where += fmt.Sprintf(` AND adapter = $%d`, len(args))
		}

		col, dir := filter.GetSort()

		query := fmt.Sprintf(
			`SELECT %s FROM sources %s ORDER BY %s %s LIMIT $1 OFFSET $2`,
			sourceColumns, where, col, dir,
		)

		rows, err := s.db.Query(ctx, query, args...)
		if err != nil {
			yield(domain.Source{}, err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			source, err := scanSource(rows)
			if err != nil {
				yield(domain.Source{}, err)
				return
			}

			if !yield(source, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(domain.Source{}, err)
		}
	}
}

// An id that is not a uuid is reported as not found.
func (s *SourceStore) GetSource(ctx context.Context, id string) (domain.Source, error) {
	source, err := scanSource(s.db.QueryRow(ctx, `SELECT `+sourceColumns+` FROM sources WHERE id = $1 AND deleted_at IS NULL`, id))
	if isInvalidText(err) {
		err = pgx.ErrNoRows
	}

	if err != nil {
		return source, fmt.Errorf("err get source %s: %w", id, err)
	}

	return source, nil
}

func scanSource(row pgx.Row) (source domain.Source, err error) {
	err = row.Scan(
		&source.ID,
		&source.DomainID,
		&source.Adapter,
		&source.URL,
		&source.Enabled,
		&source.Status,
		&source.Rate,
		&source.Lang,
		&source.Metadata,
		&source.CreatedAt,
	)

	return
}

// The domain's oldest usable source. Deleted sources count, so a domain whose sources are all disabled or deleted is blocked.
func (s *SourceStore) DomainSource(ctx context.Context, domainID string) (source domain.Source, blocked bool, err error) {
	var usable bool

	row := s.db.QueryRow(ctx, `
		SELECT `+sourceColumns+`, enabled AND deleted_at IS NULL AS usable
		FROM sources
		WHERE domain_id = $1
		ORDER BY usable DESC, created_at
		LIMIT 1
		`, domainID)

	err = row.Scan(
		&source.ID,
		&source.DomainID,
		&source.Adapter,
		&source.URL,
		&source.Enabled,
		&source.Status,
		&source.Rate,
		&source.Lang,
		&source.Metadata,
		&source.CreatedAt,
		&usable,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Source{}, false, nil
	}

	if err != nil {
		return domain.Source{}, false, fmt.Errorf("err get domain source %s: %w", domainID, err)
	}

	if !usable {
		return domain.Source{}, true, nil
	}

	return source, false, nil
}

// Enabled only so it is not blocked. A proposed source is neither scraped nor trusted.
func (s *SourceStore) CreateProposed(ctx context.Context, domainID, url, fromPostID string) (id string, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO sources (domain_id, adapter, url, enabled, status) VALUES ($1, 'page', $2, true, 'proposed')
			RETURNING id
			`, domainID, url).Scan(&id); err != nil {
			return fmt.Errorf("err insert proposed source: %w", err)
		}

		if _, err := tx.Exec(ctx, `INSERT INTO source_discovered_by_post (source_id, post_id) VALUES ($1, $2)`, id, fromPostID); err != nil {
			return fmt.Errorf("err insert source discovered by post: %w", err)
		}

		return nil
	})

	return id, err
}
