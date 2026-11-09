package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	db *database.Database
}

func NewStore(db *database.Database) Store {
	return Store{
		db: db,
	}
}

// The earliest linked post sets the search language and the own host for kept URLs.
func (s *Store) GetQuery(ctx context.Context, id string) (i SearchItem, err error) {
	var results []byte

	if err = s.db.QueryRow(ctx, `
		SELECT q.id, q.normalized_entity, q.action, q.query, q.searched_at, p.lang, p.id, d.base_url,
			q.result->'results', q.searched_at IS NOT NULL AND (q.result IS NULL OR q.result ? 'kept')
		FROM discovery_search_queries q
		CROSS JOIN LATERAL (
			SELECT p.id, p.lang, p.domain_id
			FROM discovery_search_query_posts qp
			JOIN posts p ON p.id = qp.post_id
			WHERE qp.query_id = q.id
			ORDER BY p.created_at
			LIMIT 1
		) p
		JOIN domains d ON d.id = p.domain_id
		WHERE q.id = $1
		`, id).Scan(&i.ID, &i.Entity, &i.Action, &i.Query, &i.SearchedAt, &i.Lang, &i.FromPostID, &i.BaseURL, &results, &i.Done); err != nil {
		return SearchItem{}, fmt.Errorf("err get search query: %w", err)
	}

	if results == nil {
		return i, nil
	}

	var stored []storedResult
	if err = json.Unmarshal(results, &stored); err != nil {
		return SearchItem{}, fmt.Errorf("err unmarshal results %s: %w", id, err)
	}

	i.Results = make([]SearchResult, len(stored))
	for j, r := range stored {
		i.Results[j] = r.SearchResult
		i.Results[j].URL = r.URL
	}

	return i, nil
}

// Paid results waiting for the filter go first. A query whose posts are all gone cannot be searched.
func (s *Store) ListUnfinished(ctx context.Context, windowDays, limit int) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT q.id FROM discovery_search_queries q
		WHERE (q.searched_at IS NULL OR (q.result IS NOT NULL AND NOT q.result ? 'kept'))
			AND q.created_at > now() - make_interval(days => $1)
			AND EXISTS (SELECT 1 FROM discovery_search_query_posts qp WHERE qp.query_id = q.id)
		ORDER BY q.searched_at IS NULL, q.created_at DESC
		LIMIT $2
		`, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("err query unfinished searches: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("err scan unfinished searches: %w", err)
	}

	return ids, nil
}

func (s *Store) PendingResults(ctx context.Context, windowDays int) (n int, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT count(*) FROM discovery_posts
		WHERE status = 'pending' AND discovery_search_query_id IS NOT NULL AND created_at > now() - make_interval(days => $1)
		`, windowDays).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count pending search results: %w", err)
	}

	return n, nil
}

// Rejected queries have no result and were not billed.
func (s *Store) SearchesLastDay(ctx context.Context) (n int, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT count(*) FROM discovery_search_queries
		WHERE searched_at > now() - interval '1 day' AND result IS NOT NULL
		`).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count searches: %w", err)
	}

	return n, nil
}

func (s *Store) MarkRejected(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `UPDATE discovery_search_queries SET searched_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("err mark rejected: %w", err)
	}

	return nil
}

func (s *Store) SaveResults(ctx context.Context, id string, results []SearchResult) error {
	stored := make([]storedResult, len(results))
	for i, r := range results {
		stored[i] = storedResult{URL: r.URL, SearchResult: r}
	}

	b, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("err marshal results: %w", err)
	}

	if _, err := s.db.Exec(ctx, `
		UPDATE discovery_search_queries SET searched_at = now(), result = jsonb_build_object('results', $2::jsonb)
		WHERE id = $1
		`, id, b); err != nil {
		return fmt.Errorf("err save results: %w", err)
	}

	return nil
}

// Returns the ids of the queued urls.
func (s *Store) SaveKept(ctx context.Context, item SearchItem, kept []int, urls []string) (ids []string, err error) {
	b, err := json.Marshal(kept)
	if err != nil {
		return nil, fmt.Errorf("err marshal kept: %w", err)
	}

	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE discovery_search_queries SET result = jsonb_set(result, '{kept}', $2::jsonb) WHERE id = $1`, item.ID, b); err != nil {
			return fmt.Errorf("err save kept: %w", err)
		}

		rows, err := tx.Query(ctx, `
			INSERT INTO discovery_posts (url, from_post_id, discovery_search_query_id)
			SELECT u, $2, $3 FROM unnest($1::text[]) AS u
			WHERE NOT EXISTS (SELECT 1 FROM posts p WHERE p.url = u)
			ON CONFLICT (url) DO NOTHING
			RETURNING id
			`, urls, item.FromPostID, item.ID)
		if err != nil {
			return fmt.Errorf("err queue kept results: %w", err)
		}

		if ids, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return fmt.Errorf("err scan kept results: %w", err)
		}

		return nil
	})

	return ids, err
}

func postSource(ctx context.Context, tx pgx.Tx, post domain.Post) (trusted bool, baseURL string, err error) {
	if err = tx.QueryRow(ctx, `
		SELECT s.enabled AND s.status = 'active', d.base_url
		FROM sources s, domains d
		WHERE s.id = $1 AND d.id = $2
		`, post.SourceID, post.DomainID).Scan(&trusted, &baseURL); err != nil {
		return false, "", fmt.Errorf("err get post source: %w", err)
	}

	return trusted, baseURL, nil
}

func insertLinks(ctx context.Context, tx pgx.Tx, postID string, urls []string) ([]string, error) {
	if len(urls) == 0 {
		return nil, nil
	}

	rows, err := tx.Query(ctx, `
		INSERT INTO discovery_posts (url, from_post_id)
		SELECT u, $2 FROM unnest($1::text[]) AS u
		WHERE NOT EXISTS (SELECT 1 FROM posts p WHERE p.url = u)
		ON CONFLICT (url) DO NOTHING
		RETURNING id
		`, urls, postID)
	if err != nil {
		return nil, fmt.Errorf("err insert outbound links: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("err scan outbound links: %w", err)
	}

	return ids, nil
}

func actionKeys(ctx context.Context, tx pgx.Tx) (quality map[string]bool, aliases map[string]string, err error) {
	rows, err := tx.Query(ctx, `SELECT key FROM tags WHERE type = 'quality' AND enabled AND status = 'active'`)
	if err != nil {
		return nil, nil, fmt.Errorf("err query quality tags: %w", err)
	}

	quality = make(map[string]bool)

	var key, alias string

	if _, err = pgx.ForEachRow(rows, []any{&key}, func() error {
		quality[key] = true
		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("err scan quality tags: %w", err)
	}

	rows, err = tx.Query(ctx, `SELECT alias, key FROM tag_aliases`)
	if err != nil {
		return nil, nil, fmt.Errorf("err query tag aliases: %w", err)
	}

	aliases = make(map[string]string)

	if _, err = pgx.ForEachRow(rows, []any{&alias, &key}, func() error {
		aliases[alias] = key
		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("err scan tag aliases: %w", err)
	}

	return quality, aliases, nil
}

// Held until commit, so a concurrent post sees this event's query before deciding to insert its own.
func lockEvent(ctx context.Context, tx pgx.Tx, entity, action string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1 || '/' || $2))`, entity, action); err != nil {
		return fmt.Errorf("err lock event: %w", err)
	}

	return nil
}

func recentQuery(ctx context.Context, tx pgx.Tx, entity, action string, windowDays int) (id string, found bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT id FROM discovery_search_queries
		WHERE normalized_entity = $1 AND action = $2 AND created_at > now() - make_interval(days => $3)
		ORDER BY created_at
		LIMIT 1
		`, entity, action, windowDays).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("err get recent query: %w", err)
	}

	return id, true, nil
}

// A query text seen outside the dedupe window is not searched again.
func insertQuery(ctx context.Context, tx pgx.Tx, entity, action, query string) (id string, created bool, err error) {
	err = tx.QueryRow(ctx, `
		INSERT INTO discovery_search_queries (normalized_entity, action, query) VALUES ($1, $2, $3)
		ON CONFLICT (query) DO NOTHING
		RETURNING id
		`, entity, action, query).Scan(&id)
	if err == nil {
		return id, true, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("err insert query: %w", err)
	}

	if err = tx.QueryRow(ctx, `SELECT id FROM discovery_search_queries WHERE query = $1`, query).Scan(&id); err != nil {
		return "", false, fmt.Errorf("err get conflicting query: %w", err)
	}

	return id, false, nil
}

func linkQuery(ctx context.Context, tx pgx.Tx, queryID, postID string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO discovery_search_query_posts (query_id, post_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
		`, queryID, postID); err != nil {
		return fmt.Errorf("err link query: %w", err)
	}

	return nil
}
