package postgres

import (
	"context"
	"errors"
	"slices"
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

func TestRawPostContentRoundTrip(t *testing.T) {
	_, s, base := rawPostDB(t)
	posted := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)

	base.URL = "https://a.example/1"
	base.Content = "# Rubrik\n\nText"
	base.Title = "Rubrik"
	base.Authors = []string{"A", "B"}
	base.PostedAt = &posted

	out, err := s.CreateRawPost(context.Background(), domain.RawPost{SourceID: base.SourceID, DomainID: base.DomainID, Lang: "sv", URL: base.URL, Content: base.Content, Title: base.Title, Authors: base.Authors, PostedAt: base.PostedAt})
	if err != nil {
		t.Fatal(err)
	}

	if out.Content != base.Content || out.Title != "Rubrik" || len(out.Authors) != 2 || out.PostedAt == nil || !out.PostedAt.Equal(posted) || out.RetryCount != 0 {
		t.Errorf("got %+v", out)
	}
}

func TestGetRawPost(t *testing.T) {
	_, s, base := rawPostDB(t)
	p := create(t, s, base, "https://a.example/1", 1)

	got, err := s.GetRawPost(context.Background(), p.ID)
	if err != nil || got.URL != p.URL || got.Content != "c" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestListUnenriched(t *testing.T) {
	db, s, base := rawPostDB(t)
	ctx := context.Background()

	mk := func(url, status string, retries int, age string) string {
		p := create(t, s, base, url, 1)
		if _, err := db.Exec(ctx, `UPDATE raw_posts SET status = $2, retry_count = $3, created_at = now() - $4::interval WHERE id = $1`, p.ID, status, retries, age); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}

	accepted := mk("https://a.example/accepted", domain.StatusAccepted, 0, "1 hour")
	older := mk("https://a.example/older", domain.StatusAccepted, 0, "2 hours")
	retried := mk("https://a.example/retried", domain.StatusFailed, 3, "3 hours")
	mk("https://a.example/exhausted", domain.StatusFailed, 10, "3 hours")
	mk("https://a.example/fresh", domain.StatusAccepted, 0, "1 minute")
	mk("https://a.example/rejected", domain.StatusRejected, 0, "1 hour")
	posted := mk("https://a.example/posted", domain.StatusAccepted, 0, "1 hour")

	if _, err := db.Exec(ctx, `
		INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content)
		SELECT source_id, url, lang, domain_id, 't', 's', 'c' FROM raw_posts WHERE id = $1
		`, posted); err != nil {
		t.Fatal(err)
	}

	ids, err := s.ListUnenriched(ctx, 20, 5*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{older, accepted, retried}; !slices.Equal(ids, want) {
		t.Errorf("got %v, want %v", ids, want)
	}
}

func TestListUnenrichedRetryCap(t *testing.T) {
	db, s, base := rawPostDB(t)
	ctx := context.Background()
	p := create(t, s, base, "https://a.example/1", 1)

	if _, err := db.Exec(ctx, `UPDATE raw_posts SET status = 'failed', retry_count = 10, created_at = now() - interval '1 hour' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}

	if ids, err := s.ListUnenriched(ctx, 20, 5*time.Minute, 10); err != nil || len(ids) != 0 {
		t.Errorf("got %v, %v", ids, err)
	}
}

func TestFailEnrich(t *testing.T) {
	_, s, base := rawPostDB(t)
	ctx := context.Background()
	p := create(t, s, base, "https://a.example/1", 1)

	for range 2 {
		if err := s.FailEnrich(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.GetRawPost(ctx, p.ID)
	if err != nil || got.Status != domain.StatusFailed || got.RetryCount != 2 {
		t.Errorf("status %q, retries %d, %v", got.Status, got.RetryCount, err)
	}
}
