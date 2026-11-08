package postgres

import (
	"context"
	"fmt"

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
	created_at`

type RawPostStore struct {
	db *database.Database
}

func NewRawPostStore(db *database.Database) RawPostStore {
	return RawPostStore{
		db: db,
	}
}

// Returns pgx.ErrNoRows when the url is already stored. Fields without a column are kept from p.
func (s *RawPostStore) CreateRawPost(ctx context.Context, p domain.RawPost) (domain.RawPost, error) {
	status := domain.StatusPending
	if p.Status != "" {
		status = p.Status
	}

	out, err := scanRawPost(s.db.QueryRow(ctx, `
		INSERT INTO raw_posts (source_id, url, lang, domain_id, raw_content, content, simhash, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (url) DO NOTHING
		RETURNING `+rawPostColumns,
		p.SourceID, p.URL, p.Lang, p.DomainID, p.RawContent, p.Content, p.Simhash, status,
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

func scanRawPost(row pgx.Row, p domain.RawPost) (domain.RawPost, error) {
	err := row.Scan(
		&p.ID,
		&p.SourceID,
		&p.URL,
		&p.Lang,
		&p.DomainID,
		&p.RawContent,
		&p.Content,
		&p.Simhash,
		&p.Status,
		&p.CreatedAt,
	)

	return p, err
}
