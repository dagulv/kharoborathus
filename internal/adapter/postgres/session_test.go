package postgres

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

func TestValidSession(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	valid := sha256.Sum256([]byte("valid"))
	expired := sha256.Sum256([]byte("expired"))

	if _, err := db.Exec(ctx, `
		WITH u AS (INSERT INTO users (email, password_hash) VALUES ('a@example.com', 'x') RETURNING id)
		INSERT INTO sessions (token_hash, user_id, expires_at)
		SELECT $1::bytea, id, now() + interval '1 day' FROM u
		UNION ALL SELECT $2::bytea, id, now() - interval '1 day' FROM u
		`, valid[:], expired[:]); err != nil {
		t.Fatal(err)
	}

	s := NewSessionStore(db)
	unknown := sha256.Sum256([]byte("unknown"))

	for name, c := range map[string]struct {
		hash []byte
		want bool
	}{"valid": {valid[:], true}, "expired": {expired[:], false}, "unknown": {unknown[:], false}} {
		if got, err := s.ValidSession(ctx, c.hash); err != nil || got != c.want {
			t.Errorf("%s: got %v, %v", name, got, err)
		}
	}
}

func TestCreateSession(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	expired := sha256.Sum256([]byte("expired"))

	var userID string
	if err := db.QueryRow(ctx, `
		WITH u AS (INSERT INTO users (email, password_hash) VALUES ('a@example.com', 'x') RETURNING id),
		s AS (INSERT INTO sessions (token_hash, user_id, expires_at) SELECT $1::bytea, id, now() - interval '1 day' FROM u)
		SELECT id FROM u
		`, expired[:]).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	s := NewSessionStore(db)
	h := sha256.Sum256([]byte("new"))

	if err := s.CreateSession(ctx, h[:], userID, 30); err != nil {
		t.Fatal(err)
	}

	if ok, err := s.ValidSession(ctx, h[:]); !ok || err != nil {
		t.Errorf("new session: %v, %v", ok, err)
	}

	var left int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token_hash = $1`, expired[:]).Scan(&left); err != nil || left != 0 {
		t.Errorf("expired session left: %d, %v", left, err)
	}
}

func TestDeleteSession(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	h := sha256.Sum256([]byte("valid"))

	if _, err := db.Exec(ctx, `
		WITH u AS (INSERT INTO users (email, password_hash) VALUES ('a@example.com', 'x') RETURNING id)
		INSERT INTO sessions (token_hash, user_id, expires_at) SELECT $1::bytea, id, now() + interval '1 day' FROM u
		`, h[:]); err != nil {
		t.Fatal(err)
	}

	s := NewSessionStore(db)

	if err := s.DeleteSession(ctx, h[:]); err != nil {
		t.Fatal(err)
	}

	if ok, _ := s.ValidSession(ctx, h[:]); ok {
		t.Error("session still valid")
	}

	unknown := sha256.Sum256([]byte("unknown"))
	if err := s.DeleteSession(ctx, unknown[:]); err != nil {
		t.Errorf("unknown: %v", err)
	}
}
