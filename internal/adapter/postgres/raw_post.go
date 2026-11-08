package postgres

import (
	"context"
	"encoding/json"
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
	created_at`

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

// Returns pgx.ErrNoRows when the url is already stored.
func (s *RawPostStore) CreateRawPost(ctx context.Context, p domain.RawPost) (domain.RawPost, error) {
	status := domain.StatusPending
	if p.Status != "" {
		status = p.Status
	}

	content, err := json.Marshal(rawContent{Content: p.Content, Title: p.Title, Authors: p.Authors, PostedAt: p.PostedAt})
	if err != nil {
		return p, fmt.Errorf("err marshal raw post content %s: %w", p.URL, err)
	}

	out, err := scanRawPost(s.db.QueryRow(ctx, `
		INSERT INTO raw_posts (source_id, url, lang, domain_id, raw_content, content, simhash, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (url) DO NOTHING
		RETURNING `+rawPostColumns,
		p.SourceID, p.URL, p.Lang, p.DomainID, p.RawContent, content, p.Simhash, status,
	), p)
	if err != nil {
		return out, fmt.Errorf("err create raw post %s: %w", p.URL, err)
	}

	return out, nil
}

func (s *RawPostStore) RawPostExists(ctx context.Context, url string) (exists bool, err error) {
	if err = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM raw_posts WHERE url = $1)`, url).Scan(&exists); err != nil {
		return false, fmt.Errorf("err raw post exists: %w", err)
	}

	return exists, nil
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

// Posts that keep failing go behind the rest, and the age gap keeps the job off posts the live path is handling.
func (s *RawPostStore) ListUnenriched(ctx context.Context, limit int, minAge time.Duration, maxRetries int) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id FROM raw_posts r
		WHERE (r.status = 'accepted' OR (r.status = 'failed' AND r.retry_count < $3))
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
	var content rawContent

	if err := row.Scan(
		&p.ID,
		&p.SourceID,
		&p.URL,
		&p.Lang,
		&p.DomainID,
		&p.RawContent,
		&content,
		&p.Simhash,
		&p.Status,
		&p.RetryCount,
		&p.CreatedAt,
	); err != nil {
		return p, err
	}

	p.Content, p.Title, p.Authors, p.PostedAt = content.Content, content.Title, content.Authors, content.PostedAt

	return p, nil
}
