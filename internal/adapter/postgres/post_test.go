package postgres

import (
	"context"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

func TestInsertPost(t *testing.T) {
	db, _, base := rawPostDB(t)
	ctx := context.Background()
	s := NewPostStore(db)
	p := domain.Post{SourceID: base.SourceID, DomainID: base.DomainID, URL: "https://a.example/1", Lang: "sv", Title: "t", ShortContent: "s", Content: "c", Tags: []string{"acquisition"}}

	for i, want := range []bool{true, false} {
		var inserted bool

		if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) (err error) {
			_, inserted, err = s.InsertPost(ctx, tx, p)
			return err
		}); err != nil || inserted != want {
			t.Errorf("insert %d: inserted %v, %v", i, inserted, err)
		}
	}

	if exists, err := s.PostExists(ctx, p.URL); err != nil || !exists {
		t.Errorf("exists %v, %v", exists, err)
	}
}

func TestTrusted(t *testing.T) {
	db, _, base := rawPostDB(t)
	ctx := context.Background()
	s := NewPostStore(db)

	cases := []struct {
		enabled bool
		status  string
		want    bool
	}{
		{true, domain.StatusActive, true},
		{false, domain.StatusActive, false},
		{true, domain.StatusProposed, false},
	}

	for _, c := range cases {
		if _, err := db.Exec(ctx, `UPDATE sources SET enabled = $2, status = $3 WHERE id = $1`, base.SourceID, c.enabled, c.status); err != nil {
			t.Fatal(err)
		}

		if got, err := s.Trusted(ctx, base.SourceID); err != nil || got != c.want {
			t.Errorf("%v %s: got %v, %v", c.enabled, c.status, got, err)
		}
	}
}
