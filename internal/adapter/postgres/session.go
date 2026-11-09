package postgres

import (
	"context"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/lib/database"
)

type SessionStore struct {
	db *database.Database
}

func NewSessionStore(db *database.Database) SessionStore {
	return SessionStore{
		db: db,
	}
}

func (s *SessionStore) ValidSession(ctx context.Context, tokenHash []byte) (valid bool, err error) {
	if err = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE token_hash = $1 AND expires_at > now())`, tokenHash).Scan(&valid); err != nil {
		return false, fmt.Errorf("err check session: %w", err)
	}

	return valid, nil
}
