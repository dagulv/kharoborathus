package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const (
	duplicateWindowDays  = 7
	maxDuplicateDistance = 3
)

const rawPostColumns = `
	id,
	source_id,
	url,
	lang,
	domain_id,
	raw_content,
	content,
	simhash,
	status,
	retry_count,
	created_at,
	discovery_post_id`

// The content column, which carries what the fetch and extraction found until the post is created.
type rawContent struct {
	Content  string     `json:"content"`
	Title    string     `json:"title"`
	Authors  []string   `json:"authors"`
	PostedAt *time.Time `json:"posted_at,omitempty"`
}

type RawPostStore struct {
	db *database.Database
}

func NewRawPostStore(db *database.Database) RawPostStore {
	return RawPostStore{
		db: db,
	}
}

// An empty SourceID or DiscoveryPostID is stored as NULL.
type Claim struct {
	URL             string
	Lang            string
	SourceID        string
	DiscoveryPostID string
}

// ClaimRawPost stores the url as pending before it is fetched, so every step can log against its id.
// A url already stored is claimed again only when it stopped before it had content, as a link
// with no row always was, or when the same discovered link retries after an error.
// claimed is false when another sync owns the url or it is done.
func (s *RawPostStore) ClaimRawPost(ctx context.Context, c Claim) (p domain.RawPost, claimed bool, err error) {
	baseURL, err := domain.BaseURL(c.URL)
	if err != nil {
		return p, false, fmt.Errorf("err claim raw post %s: %w", c.URL, err)
	}

	p, err = scanRawPost(s.db.QueryRow(ctx, `
		WITH d AS (
			INSERT INTO domains (base_url) VALUES ($1)
			ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url
			RETURNING id
		)
		INSERT INTO raw_posts (source_id, discovery_post_id, url, lang, domain_id)
		SELECT NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, d.id FROM d
		ON CONFLICT (url) DO UPDATE SET status = 'pending', created_at = now()
		WHERE raw_posts.content IS NULL AND (
			raw_posts.status IN ('failed', 'missing')
			OR (raw_posts.status = 'pending' AND raw_posts.created_at < now() - interval '5 minutes')
			OR (raw_posts.status = 'pending' AND raw_posts.discovery_post_id = EXCLUDED.discovery_post_id)
		)
		RETURNING `+rawPostColumns,
		baseURL, c.SourceID, c.DiscoveryPostID, c.URL, c.Lang,
	), domain.RawPost{})
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, nil
	}

	if err != nil {
		return p, false, fmt.Errorf("err claim raw post %s: %w", c.URL, err)
	}

	return p, true, nil
}

// An empty SourceID keeps the stored one. created_at moves to now, since the duplicate check and
// the catch-up jobs count a post's age from when its content arrived.
func (s *RawPostStore) SaveContent(ctx context.Context, p domain.RawPost) (domain.RawPost, error) {
	content, err := json.Marshal(rawContent{Content: p.Content, Title: p.Title, Authors: p.Authors, PostedAt: p.PostedAt})
	if err != nil {
		return p, fmt.Errorf("err marshal raw post content %s: %w", p.URL, err)
	}

	if err := s.db.QueryRow(ctx, `
		UPDATE raw_posts
		SET source_id = coalesce(NULLIF($2, '')::uuid, source_id), domain_id = $3, lang = $4, raw_content = $5, content = $6, simhash = $7, created_at = now()
		WHERE id = $1
		RETURNING created_at
		`, p.ID, p.SourceID, p.DomainID, p.Lang, p.RawContent, content, p.Simhash).Scan(&p.CreatedAt); err != nil {
		return p, fmt.Errorf("err save raw post content %s: %w", p.URL, err)
	}

	return p, nil
}

// Only earlier rows count, so of two near-identical posts exactly one is the duplicate.
func (s *RawPostStore) HasDuplicate(ctx context.Context, p domain.RawPost) (exists bool, err error) {
	if p.Simhash == nil {
		return false, nil
	}

	if err = s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM raw_posts
			WHERE (created_at, id) < ($1, $2)
				AND created_at > $1 - make_interval(days => $4)
				AND bit_count((simhash # $3::bigint)::bit(64)) <= $5
		)
		`, p.CreatedAt, p.ID, *p.Simhash, duplicateWindowDays, maxDuplicateDistance).Scan(&exists); err != nil {
		return false, fmt.Errorf("err check duplicate %s: %w", p.URL, err)
	}

	return exists, nil
}

func (s *RawPostStore) SetStatus(ctx context.Context, id, status string) error {
	if _, err := s.db.Exec(ctx, `UPDATE raw_posts SET status = $2 WHERE id = $1`, id, status); err != nil {
		return fmt.Errorf("err set raw post status: %w", err)
	}

	return nil
}

func (s *RawPostStore) GetRawPost(ctx context.Context, id string) (domain.RawPost, error) {
	p, err := scanRawPost(s.db.QueryRow(ctx, `SELECT `+rawPostColumns+` FROM raw_posts WHERE id = $1`, id), domain.RawPost{})
	if err != nil {
		return p, fmt.Errorf("err get raw post %s: %w", id, err)
	}

	return p, nil
}

// The age gap keeps the job off posts the live pipeline is handling.
func (s *RawPostStore) ListPending(ctx context.Context, limit int, minAge time.Duration) ([]domain.RawPost, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+rawPostColumns+` FROM raw_posts
		WHERE status = 'pending' AND content IS NOT NULL AND created_at < now() - $2::interval
		ORDER BY created_at
		LIMIT $1
		`, limit, minAge)
	if err != nil {
		return nil, fmt.Errorf("err query pending raw posts: %w", err)
	}

	posts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.RawPost, error) {
		return scanRawPost(row, domain.RawPost{})
	})
	if err != nil {
		return nil, fmt.Errorf("err scan pending raw posts: %w", err)
	}

	return posts, nil
}

// Posts that keep failing go behind the rest, and the age gap keeps the job off posts the live path is handling.
func (s *RawPostStore) ListUnenriched(ctx context.Context, limit int, minAge time.Duration, maxRetries int) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id FROM raw_posts r
		WHERE (r.status = 'accepted' OR (r.status = 'failed' AND r.retry_count < $3))
			AND r.content IS NOT NULL
			AND r.created_at < now() - $2::interval
			AND NOT EXISTS (SELECT 1 FROM posts p WHERE p.url = r.url)
		ORDER BY r.retry_count, r.created_at
		LIMIT $1
		`, limit, minAge, maxRetries)
	if err != nil {
		return nil, fmt.Errorf("err query unenriched raw posts: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("err scan unenriched raw posts: %w", err)
	}

	return ids, nil
}

func (s *RawPostStore) FailEnrich(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `UPDATE raw_posts SET status = 'failed', retry_count = retry_count + 1 WHERE id = $1`, id); err != nil {
		return fmt.Errorf("err fail enrich: %w", err)
	}

	return nil
}

func scanRawPost(row pgx.Row, p domain.RawPost) (domain.RawPost, error) {
	var sourceID, rawContentText, discoveryPostID sql.NullString
	var content *rawContent

	if err := row.Scan(
		&p.ID,
		&sourceID,
		&p.URL,
		&p.Lang,
		&p.DomainID,
		&rawContentText,
		&content,
		&p.Simhash,
		&p.Status,
		&p.RetryCount,
		&p.CreatedAt,
		&discoveryPostID,
	); err != nil {
		return p, err
	}

	p.SourceID = sourceID.String
	p.DiscoveryPostID = discoveryPostID.String
	p.RawContent = rawContentText.String

	if content == nil {
		return p, nil
	}

	p.Content, p.Title, p.Authors, p.PostedAt = content.Content, content.Title, content.Authors, content.PostedAt

	return p, nil
}
