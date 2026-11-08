package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type PostStore struct {
	db *database.Database
}

func NewPostStore(db *database.Database) PostStore {
	return PostStore{
		db: db,
	}
}

func (s *PostStore) PostExists(ctx context.Context, url string) (exists bool, err error) {
	if err = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE url = $1)`, url).Scan(&exists); err != nil {
		return false, fmt.Errorf("err post exists: %w", err)
	}

	return exists, nil
}

// Inserted is false when another worker stored the url first.
func (s *PostStore) InsertPost(ctx context.Context, tx pgx.Tx, p domain.Post) (out domain.Post, inserted bool, err error) {
	err = tx.QueryRow(ctx, `
		INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content, tags, posted_at, authors, simhash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], '{}'), $9, COALESCE($10::text[], '{}'), $11)
		ON CONFLICT (url) DO NOTHING
		RETURNING id, created_at
		`, p.SourceID, p.URL, p.Lang, p.DomainID, p.Title, p.ShortContent, p.Content, p.Tags, p.PostedAt, p.Authors, p.Simhash).Scan(&p.ID, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, nil
	}

	if err != nil {
		return p, false, fmt.Errorf("err insert post %s: %w", p.URL, err)
	}

	return p, true, nil
}

func (s *PostStore) Trusted(ctx context.Context, sourceID string) (trusted bool, err error) {
	if err = s.db.QueryRow(ctx, `SELECT enabled AND status = 'active' FROM sources WHERE id = $1`, sourceID).Scan(&trusted); err != nil {
		return false, fmt.Errorf("err get source trust: %w", err)
	}

	return trusted, nil
}
