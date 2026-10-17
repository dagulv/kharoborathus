package source

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"time"

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

// Soft deletes, so posts keep their source and ingest never proposes the domain again. Disabling it drops trust everywhere.
func (s *Store) DeleteSource(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `UPDATE sources SET deleted_at = now(), enabled = false WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("err delete source: %w", err)
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

// A source must also fail for a day, so a short outage of the server's network cannot put every source to sleep for a probe interval.
func (s *Store) RecordFetch(ctx context.Context, id string, ok bool, maxFailures int, minFailing time.Duration) error {
	if ok {
		if _, err := s.db.Exec(ctx, `UPDATE sources SET fetch_failures = 0, failing_since = NULL WHERE id = $1 AND fetch_failures > 0`, id); err != nil {
			return fmt.Errorf("err reset fetch failures: %w", err)
		}

		return nil
	}

	if _, err := s.db.Exec(ctx, `
		UPDATE sources
		SET
			fetch_failures = fetch_failures + 1,
			failing_since = coalesce(failing_since, now()),
			status = CASE WHEN status = 'active' AND fetch_failures + 1 >= $2 AND failing_since < now() - $3::interval THEN 'dormant' ELSE status END,
			status_changed_at = CASE WHEN status = 'active' AND fetch_failures + 1 >= $2 AND failing_since < now() - $3::interval THEN now() ELSE status_changed_at END
		WHERE id = $1
		`, id, maxFailures, minFailing); err != nil {
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

func (s *Store) MarkDormant(ctx context.Context, windowDays int) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE sources s
		SET status = 'dormant', status_changed_at = now()
		WHERE s.status = 'active'
			AND s.enabled
			AND s.deleted_at IS NULL
			AND greatest(s.status_changed_at, (SELECT max(p.created_at) FROM posts p WHERE p.source_id = s.id)) < now() - make_interval(days => $1)
		`, windowDays)
	if err != nil {
		return 0, fmt.Errorf("err mark dormant sources: %w", err)
	}

	return tag.RowsAffected(), nil
}

type Promotable struct {
	ID  string
	URL string
}

func (s *Store) PromotedLastWeek(ctx context.Context) (n int, err error) {
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM sources WHERE promoted_at > now() - interval '7 days'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count promotions: %w", err)
	}

	return n, nil
}

// A demoted source counts only evidence since its demotion. Days count when links were found, so one burst cannot pass.
func (s *Store) ListPromotable(ctx context.Context, minAccepted, minDays int, minRate float64, limit int) ([]Promotable, error) {
	rows, err := s.db.Query(ctx, `
		SELECT s.id, d.base_url
		FROM sources s
		JOIN domains d ON d.id = s.domain_id
		JOIN LATERAL (
			SELECT
				count(*) FILTER (WHERE q.status = 'accepted') AS accepted,
				count(DISTINCT q.created_at::date) FILTER (WHERE q.status = 'accepted') AS days,
				count(*) FILTER (WHERE q.status IN ('accepted', 'rejected')) AS decided
			FROM link_queue q
			JOIN domains qd ON qd.id = q.domain_id
			-- Ingest attaches http and https links to one source, so evidence counts both.
			WHERE split_part(qd.base_url, '://', 2) = split_part(d.base_url, '://', 2)
				AND q.checked_at >= CASE WHEN s.promoted_at IS NULL THEN '-infinity' ELSE s.status_changed_at END
		) e ON true
		WHERE s.status = 'proposed'
			AND s.enabled
			AND s.deleted_at IS NULL
			AND e.accepted >= $1
			AND e.days >= $2
			AND e.accepted >= $3::float8 * e.decided
		ORDER BY s.created_at
		LIMIT $4
		`, minAccepted, minDays, minRate, limit)
	if err != nil {
		return nil, fmt.Errorf("err query promotable sources: %w", err)
	}

	sources, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (p Promotable, err error) {
		err = row.Scan(&p.ID, &p.URL)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan promotable sources: %w", err)
	}

	return sources, nil
}

// Reports false when the source left proposed since it was listed.
func (s *Store) Promote(ctx context.Context, id, adapterName, url string) (bool, error) {
	// reviewed_at makes the shadow gate judge the source on posts after promotion only.
	tag, err := s.db.Exec(ctx, `
		UPDATE sources
		SET status = 'active', adapter = $2, url = $3, status_changed_at = now(), promoted_at = now(),
			flagged_at = NULL, reviewed_at = now(), fetch_failures = 0, failing_since = NULL
		WHERE id = $1 AND status = 'proposed'
		`, id, adapterName, url)
	if err != nil {
		return false, fmt.Errorf("err promote source: %w", err)
	}

	return tag.RowsAffected() > 0, nil
}
