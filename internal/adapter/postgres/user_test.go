package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/jackc/pgx/v5"
)

func TestUserByEmail(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	s := NewUserStore(db)

	if err := s.CreateUser(ctx, "a@example.com", "salt:key"); err != nil {
		t.Fatal(err)
	}

	u, err := s.UserByEmail(ctx, "a@example.com")
	if err != nil || u.ID == "" || u.Email != "a@example.com" || u.PasswordHash != "salt:key" {
		t.Errorf("got %+v, %v", u, err)
	}

	if _, err := s.UserByEmail(ctx, "b@example.com"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown: %v", err)
	}
}
