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
