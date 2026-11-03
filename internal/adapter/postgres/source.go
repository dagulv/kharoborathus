package postgres

import (
	"context"
	"fmt"
	"iter"
	"time"

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

func (s *SourceStore) GetSource(ctx context.Context, id string) (domain.Source, error) {
	return scanSource(s.db.QueryRow(ctx, `SELECT `+sourceColumns+` FROM sources WHERE id = $1 AND deleted_at IS NULL`, id))
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
		&source.Metadata,
		&source.CreatedAt,
	)

	return
}

// Only scraped sources are backfilled, so a proposed source waits for its promotion.
func (s *SourceStore) ListUnbackfilled(ctx context.Context, limit int) ([]domain.Source, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+sourceColumns+` FROM sources
		WHERE backfilled_at IS NULL AND enabled AND status = 'active' AND deleted_at IS NULL
		ORDER BY created_at
		LIMIT $1
		`, limit)
	if err != nil {
		return nil, fmt.Errorf("err query unbackfilled sources: %w", err)
	}

	sources, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Source, error) {
		return scanSource(row)
	})
	if err != nil {
		return nil, fmt.Errorf("err scan unbackfilled sources: %w", err)
	}

	return sources, nil
}

func (s *SourceStore) MarkBackfilled(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `UPDATE sources SET backfilled_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("err mark backfilled: %w", err)
	}

	return nil
}

// A source must also fail for a day, so a short outage of the server's network cannot put every source to sleep for a probe interval.
func (s *SourceStore) RecordFetch(ctx context.Context, id string, ok bool, maxFailures int, minFailing time.Duration) error {
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

func (s *SourceStore) Revive(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE sources SET status = 'active', status_changed_at = now()
		WHERE id = $1 AND status = 'dormant'
		`, id); err != nil {
		return fmt.Errorf("err revive source: %w", err)
	}

	return nil
}

func (s *SourceStore) MarkDormant(ctx context.Context, windowDays int) (int64, error) {
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

func (s *SourceStore) PromotedLastWeek(ctx context.Context) (n int, err error) {
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM sources WHERE promoted_at > now() - interval '7 days'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count promotions: %w", err)
	}

	return n, nil
}

// A demoted source counts only evidence since its demotion. Days count when links were found, so one burst cannot pass.
func (s *SourceStore) ListPromotable(ctx context.Context, minAccepted, minDays int, minRate float64, limit int) ([]Promotable, error) {
	rows, err := s.db.Query(ctx, `
		SELECT s.id, d.base_url
		FROM sources s
		JOIN domains d ON d.id = s.domain_id
		JOIN LATERAL (
			SELECT
				count(*) FILTER (WHERE q.status = 'accepted') AS accepted,
				count(DISTINCT q.created_at::date) FILTER (WHERE q.status = 'accepted') AS days,
				count(*) FILTER (WHERE q.status IN ('accepted', 'rejected')) AS decided
			FROM link_events q
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
func (s *SourceStore) Promote(ctx context.Context, id, adapterName, url string) (bool, error) {
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
