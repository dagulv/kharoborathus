package postgres

import (
	"context"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
)

type UserStore struct {
	db *database.Database
}

func NewUserStore(db *database.Database) UserStore {
	return UserStore{
		db: db,
	}
}

func (s *UserStore) UserByEmail(ctx context.Context, email string) (u domain.User, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT id, email, password_hash, created_at FROM users WHERE email = $1
		`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt); err != nil {
		return u, fmt.Errorf("err get user: %w", err)
	}

	return u, nil
}

func (s *UserStore) CreateUser(ctx context.Context, email, passwordHash string) error {
	if _, err := s.db.Exec(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, $2)`, email, passwordHash); err != nil {
		return fmt.Errorf("err create user: %w", err)
	}

	return nil
}
