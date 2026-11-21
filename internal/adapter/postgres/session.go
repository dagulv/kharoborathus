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

// Expired sessions are cleared on each login, so the table never needs a sweeper.
func (s *SessionStore) CreateSession(ctx context.Context, tokenHash []byte, userID string, days int) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`); err != nil {
		return fmt.Errorf("err delete expired sessions: %w", err)
	}

	if _, err := s.db.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ($1, $2, now() + make_interval(days => $3))
		`, tokenHash, userID, days); err != nil {
		return fmt.Errorf("err create session: %w", err)
	}

	return nil
}

func (s *SessionStore) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("err delete session: %w", err)
	}

	return nil
}
