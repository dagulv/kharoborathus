package postgres

import (
	"context"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
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
