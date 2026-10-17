package ingest

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	db *database.Database
}

func NewStore(db *database.Database) Store {
	return Store{db: db}
}

func (s *Store) ListUnscannedPosts(ctx context.Context, windowDays, limit int) ([]ScanPost, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.content, d.base_url
		FROM posts p
		JOIN sources s ON s.id = p.source_id
		JOIN domains d ON d.id = p.domain_id
		WHERE s.status IN ('active', 'dormant')
			AND s.enabled
			AND p.created_at > now() - make_interval(days => $1)
			AND NOT EXISTS (SELECT 1 FROM link_checked c WHERE c.post_id = p.id)
		ORDER BY p.created_at
		LIMIT $2
		`, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("err query unscanned posts: %w", err)
	}

	posts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (p ScanPost, err error) {
		err = row.Scan(&p.ID, &p.Content, &p.BaseURL)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan unscanned posts: %w", err)
	}

	return posts, nil
}

func (s *Store) QueueLinks(ctx context.Context, postID string, urls []string) (queued int64, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO link_queue (url, via, from_post_id)
			SELECT u, 'link', $2 FROM unnest($1::text[]) AS u
			WHERE NOT EXISTS (SELECT 1 FROM posts p WHERE p.url = u)
			ON CONFLICT (url) DO NOTHING
			`, urls, postID)
		if err != nil {
			return fmt.Errorf("err queue links: %w", err)
		}

		queued = tag.RowsAffected()

		if _, err := tx.Exec(ctx, `INSERT INTO link_checked (post_id) VALUES ($1) ON CONFLICT (post_id) DO NOTHING`, postID); err != nil {
			return fmt.Errorf("err mark link checked: %w", err)
		}

		return nil
	})

	return
}

func (s *Store) CheckedLastDay(ctx context.Context) (n int, err error) {
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM link_queue WHERE checked_at > now() - interval '1 day'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count checked links: %w", err)
	}

	return n, nil
}

// One link per host, so a tick never hits a domain twice. Retries come after fresh links.
func (s *Store) ListPending(ctx context.Context, windowDays, limit int) ([]Link, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, url, via, from_post_id
		FROM (
			SELECT DISTINCT ON (host) id, url, via, from_post_id, checked_at, created_at
			FROM (
				SELECT *, lower(substring(url FROM '^[^:]+://(?:www\.)?([^/:?#]+)')) AS host
				FROM link_queue
				WHERE status = 'pending' AND created_at > now() - make_interval(days => $1)
			) q
			ORDER BY host, checked_at NULLS FIRST, created_at
		) h
		ORDER BY checked_at NULLS FIRST, created_at
		LIMIT $2
		`, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("err query pending links: %w", err)
	}

	links, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (l Link, err error) {
		err = row.Scan(&l.ID, &l.URL, &l.Via, &l.FromPostID)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan pending links: %w", err)
	}

	return links, nil
}

func (s *Store) Finish(ctx context.Context, id, status, domainID string, scores any, reason string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE link_queue
		SET
			status = $2,
			domain_id = coalesce(NULLIF($3, '')::uuid, domain_id),
			gate_scores = coalesce($4::jsonb, gate_scores),
			error = NULLIF($5, ''),
			attempts = attempts + 1,
			checked_at = now()
		WHERE id = $1
		`, id, status, domainID, scores, reason); err != nil {
		return fmt.Errorf("err finish link: %w", err)
	}

	return nil
}

func (s *Store) Retry(ctx context.Context, id, domainID, reason string, maxAttempts int) (failed bool, err error) {
	if err = s.db.QueryRow(ctx, `
		UPDATE link_queue
		SET
			status = CASE WHEN attempts + 1 >= $4 THEN 'failed' ELSE status END,
			domain_id = coalesce(NULLIF($2, '')::uuid, domain_id),
			error = $3,
			attempts = attempts + 1,
			checked_at = now()
		WHERE id = $1
		RETURNING status = 'failed'
		`, id, domainID, reason, maxAttempts).Scan(&failed); err != nil {
		return false, fmt.Errorf("err retry link: %w", err)
	}

	return failed, nil
}

var errSourceDisabled = errors.New("ingest: source disabled")

// A domain whose sources are all disabled or deleted is blocked, so you can stop a domain without a code change.
// An enabled source wins over older disabled ones, so a manual feed can replace a disabled discovered source.
const oldestSourceSQL = `
	SELECT id, enabled AND deleted_at IS NULL FROM sources WHERE domain_id = $1
	ORDER BY enabled AND deleted_at IS NULL DESC, created_at
	LIMIT 1`

func (s *Store) DomainAllowed(ctx context.Context, domainID string) (bool, error) {
	var id string

	allowed := true

	err := s.db.QueryRow(ctx, oldestSourceSQL, domainID).Scan(&id, &allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("err check domain source: %w", err)
	}

	return allowed, nil
}

// Rechecks the source inside the transaction, since it can change during the paid steps.
func (s *Store) Accept(ctx context.Context, link Link, f feed.Feed, scores any) (post feed.Feed, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		allowed := true

		err := tx.QueryRow(ctx, oldestSourceSQL, f.DomainID).Scan(&f.SourceID, &allowed)
		if errors.Is(err, pgx.ErrNoRows) {
			f.SourceID, err = proposeSource(ctx, tx, f.DomainID, link.FromPostID)
		}

		if err != nil {
			return fmt.Errorf("err resolve source: %w", err)
		}

		if !allowed {
			return errSourceDisabled
		}

		post, err = feed.InsertFeed(ctx, tx, f)
		if err != nil {
			return fmt.Errorf("err insert post: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO post_links (from_post_id, to_post_id, relation, metadata)
			VALUES ($1, $2, 'discovered', jsonb_build_object('via', $3::text))
			`, post.ID, link.FromPostID, link.Via); err != nil {
			return fmt.Errorf("err link discovered: %w", err)
		}

		if _, err := tx.Exec(ctx, `INSERT INTO tag_checked (post_id) VALUES ($1)`, post.ID); err != nil {
			return fmt.Errorf("err mark tag checked: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE link_queue
			SET status = 'accepted', domain_id = $2, gate_scores = $3, error = NULL, attempts = attempts + 1, checked_at = now()
			WHERE id = $1
			`, link.ID, f.DomainID, scores); err != nil {
			return fmt.Errorf("err accept link: %w", err)
		}

		return nil
	})

	return
}

func proposeSource(ctx context.Context, tx pgx.Tx, domainID, fromPostID string) (id string, err error) {
	// enabled defaults to false, so it must be set or the source could never be promoted into scraping.
	if err = tx.QueryRow(ctx, `
		INSERT INTO sources (domain_id, adapter, url, enabled, origin, status)
		SELECT id, 'page', base_url, true, 'discovered', 'proposed' FROM domains WHERE id = $1
		RETURNING id
		`, domainID).Scan(&id); err != nil {
		return "", fmt.Errorf("err propose source: %w", err)
	}

	if _, err = tx.Exec(ctx, `INSERT INTO source_discovered_by_post (source_id, post_id) VALUES ($1, $2)`, id, fromPostID); err != nil {
		return "", fmt.Errorf("err record discovering post: %w", err)
	}

	return id, nil
}
