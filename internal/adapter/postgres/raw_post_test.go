package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/jackc/pgx/v5"
)

func rawPostDB(t *testing.T) (*database.Database, RawPostStore, domain.RawPost) {
	db := dbtest.New(t)
	ctx := context.Background()

	var p domain.RawPost

	if err := db.QueryRow(ctx, `INSERT INTO domains (base_url) VALUES ('https://a.example') RETURNING id`).Scan(&p.DomainID); err != nil {
		t.Fatal(err)
	}

	if err := db.QueryRow(ctx, `INSERT INTO sources (domain_id, adapter, url) VALUES ($1, 'rss', 'https://a.example/feed') RETURNING id`, p.DomainID).Scan(&p.SourceID); err != nil {
		t.Fatal(err)
	}

	p.Lang = "sv"
	p.Content = "c"

	return db, NewRawPostStore(db), p
}

func create(t *testing.T, s RawPostStore, p domain.RawPost, url string, hash int64) domain.RawPost {
	t.Helper()

	p.URL = url
	p.Simhash = &hash

	out, err := s.CreateRawPost(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func TestHasDuplicate(t *testing.T) {
	db, s, base := rawPostDB(t)
	ctx := context.Background()

	first := create(t, s, base, "https://a.example/1", 0b1111)
	near := create(t, s, base, "https://a.example/2", 0b1000)
	far := create(t, s, base, "https://a.example/3", 0b11110000)

	cases := map[string]struct {
		post domain.RawPost
		want bool
	}{
		"first of its kind":    {first, false},
		"within three bits":    {near, true},
		"more than three bits": {far, false},
		"no simhash":           {domain.RawPost{ID: near.ID, CreatedAt: near.CreatedAt}, false},
	}

	for name, c := range cases {
		if got, err := s.HasDuplicate(ctx, c.post); err != nil || got != c.want {
			t.Errorf("%s: got %v, %v", name, got, err)
		}
	}

	if _, err := db.Exec(ctx, `UPDATE raw_posts SET created_at = now() - interval '8 days' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}

	if got, err := s.HasDuplicate(ctx, near); err != nil || got {
		t.Errorf("outside window: got %v, %v", got, err)
	}
}

func TestHasDuplicateSameInstant(t *testing.T) {
	db, s, base := rawPostDB(t)
	ctx := context.Background()

	a := create(t, s, base, "https://a.example/1", 0b1111)
	b := create(t, s, base, "https://a.example/2", 0b1111)

	at := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Exec(ctx, `UPDATE raw_posts SET created_at = $1`, at); err != nil {
		t.Fatal(err)
	}

	a.CreatedAt, b.CreatedAt = at, at

	gotA, errA := s.HasDuplicate(ctx, a)
	gotB, errB := s.HasDuplicate(ctx, b)

	if errA != nil || errB != nil || gotA == gotB {
		t.Errorf("exactly one must be the duplicate: a %v, b %v, %v %v", gotA, gotB, errA, errB)
	}
}

func TestSetStatus(t *testing.T) {
	db, s, base := rawPostDB(t)
	ctx := context.Background()
	p := create(t, s, base, "https://a.example/1", 1)

	if err := s.SetStatus(ctx, p.ID, domain.StatusAccepted); err != nil {
		t.Fatal(err)
	}

	var status string
	if err := db.QueryRow(ctx, `SELECT status FROM raw_posts WHERE id = $1`, p.ID).Scan(&status); err != nil || status != domain.StatusAccepted {
		t.Errorf("status %q, %v", status, err)
	}
}

func TestCreateRawPostExists(t *testing.T) {
	_, s, base := rawPostDB(t)
	create(t, s, base, "https://a.example/1", 1)

	base.URL = "https://a.example/1"
	if _, err := s.CreateRawPost(context.Background(), base); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("err %v", err)
	}
}
