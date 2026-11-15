package postgres

import (
	"context"
	"errors"
	"fmt"
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

	ctx := context.Background()

	out, _, err := s.ClaimRawPost(ctx, Claim{URL: url, Lang: p.Lang, SourceID: p.SourceID})
	if err != nil {
		t.Fatal(err)
	}

	out.Lang, out.Content, out.Simhash = p.Lang, p.Content, &hash

	if err := s.SaveContent(ctx, out); err != nil {
		t.Fatal(err)
	}

	out, err = s.GetRawPost(ctx, out.ID)
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

func TestGetRawPost(t *testing.T) {
	_, s, base := rawPostDB(t)
	p := create(t, s, base, "https://a.example/1", 1)

	got, err := s.GetRawPost(context.Background(), p.ID)
	if err != nil || got.URL != p.URL || got.Content != "c" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestListPending(t *testing.T) {
	db, s, base := rawPostDB(t)
	ctx := context.Background()

	mk := func(url, status, age string) string {
		p := create(t, s, base, url, 1)
		if _, err := db.Exec(ctx, `UPDATE raw_posts SET status = $2, created_at = now() - $3::interval WHERE id = $1`, p.ID, status, age); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}

	pending := mk("https://a.example/pending", domain.StatusPending, "1 hour")
	older := mk("https://a.example/older", domain.StatusPending, "2 hours")
	mk("https://a.example/fresh", domain.StatusPending, "1 minute")
	mk("https://a.example/accepted", domain.StatusAccepted, "1 hour")

	posts, err := s.ListPending(ctx, 20, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	ids := make([]string, len(posts))
	for i, p := range posts {
		ids[i] = p.ID
	}

	if want := []string{older, pending}; !slices.Equal(ids, want) || posts[0].Content != "c" {
		t.Errorf("got %v, want %v", ids, want)
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

func TestGetSourceInvalidID(t *testing.T) {
	db, _, _ := rawPostDB(t)
	s := NewSourceStore(db)

	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "nope"} {
		if _, err := s.GetSource(context.Background(), id); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s: err %v", id, err)
		}
	}
}

func TestClaimRawPost(t *testing.T) {
	db, s, base := rawPostDB(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		status  string
		content bool
		age     string
		want    bool
	}{
		{"failed fetch", domain.StatusFailed, false, "1 hour", true},
		{"missing content", domain.StatusMissing, false, "1 hour", true},
		{"failed enrich", domain.StatusFailed, true, "1 hour", false},
		{"accepted", domain.StatusAccepted, true, "1 hour", false},
		{"rejected", domain.StatusRejected, true, "1 hour", false},
		{"fresh pending", domain.StatusPending, false, "1 minute", false},
		{"stale pending", domain.StatusPending, false, "10 minutes", true},
	}

	for i, c := range cases {
		url := fmt.Sprintf("https://a.example/%d", i)

		first, ok, err := s.ClaimRawPost(ctx, Claim{URL: url, Lang: "sv", SourceID: base.SourceID})
		if err != nil || !ok {
			t.Fatalf("%s: first claim %v, %v", c.name, ok, err)
		}

		content := any(nil)
		if c.content {
			content = `{"content":"c"}`
		}

		if _, err := db.Exec(ctx, `UPDATE raw_posts SET status = $2, content = $3::jsonb, created_at = now() - $4::interval WHERE id = $1`, first.ID, c.status, content, c.age); err != nil {
			t.Fatal(err)
		}

		again, ok, err := s.ClaimRawPost(ctx, Claim{URL: url, Lang: "sv", SourceID: base.SourceID})
		if err != nil || ok != c.want {
			t.Errorf("%s: claimed %v, %v", c.name, ok, err)
			continue
		}

		if c.want && (again.ID != first.ID || again.Status != domain.StatusPending) {
			t.Errorf("%s: reclaimed %+v", c.name, again)
		}
	}
}

func TestClaimRawPostNew(t *testing.T) {
	_, s, base := rawPostDB(t)
	ctx := context.Background()

	p, ok, err := s.ClaimRawPost(ctx, Claim{URL: "https://b.example/new"})
	if err != nil || !ok {
		t.Fatalf("claim %v, %v", ok, err)
	}

	if p.Status != domain.StatusPending || p.DomainID == "" || p.DomainID == base.DomainID || p.SourceID != "" || p.DiscoveryPostID != "" {
		t.Errorf("got %+v", p)
	}

	got, err := s.GetRawPost(ctx, p.ID)
	if err != nil || got.SourceID != "" || got.Content != "" {
		t.Errorf("get %+v, %v", got, err)
	}
}

func TestClaimRawPostOnce(t *testing.T) {
	_, s, base := rawPostDB(t)
	ctx := context.Background()
	c := Claim{URL: "https://a.example/once", SourceID: base.SourceID}

	if _, ok, err := s.ClaimRawPost(ctx, c); err != nil || !ok {
		t.Fatalf("first %v, %v", ok, err)
	}

	if _, ok, err := s.ClaimRawPost(ctx, c); err != nil || ok {
		t.Errorf("second %v, %v", ok, err)
	}
}

func TestSaveContent(t *testing.T) {
	_, s, base := rawPostDB(t)
	ctx := context.Background()
	posted := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)
	hash := int64(7)

	p, _, err := s.ClaimRawPost(ctx, Claim{URL: "https://a.example/save"})
	if err != nil {
		t.Fatal(err)
	}

	p.SourceID = base.SourceID
	p.Lang = "en"
	p.RawContent = "<p>x</p>"
	p.Content = "x"
	p.Title = "T"
	p.Authors = []string{"A"}
	p.PostedAt = &posted
	p.Simhash = &hash

	if err := s.SaveContent(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetRawPost(ctx, p.ID)
	if err != nil || got.SourceID != base.SourceID || got.Lang != "en" || got.RawContent != "<p>x</p>" || got.Content != "x" || got.Title != "T" || len(got.Authors) != 1 || !got.PostedAt.Equal(posted) || *got.Simhash != 7 {
		t.Fatalf("got %+v, %v", got, err)
	}

	p.SourceID = ""
	if err := s.SaveContent(ctx, p); err != nil {
		t.Fatal(err)
	}

	if got, err := s.GetRawPost(ctx, p.ID); err != nil || got.SourceID != base.SourceID {
		t.Errorf("source kept: %+v, %v", got, err)
	}
}

func TestListsSkipRowsWithoutContent(t *testing.T) {
	db, s, base := rawPostDB(t)
	ctx := context.Background()

	for _, status := range []string{domain.StatusPending, domain.StatusFailed} {
		p, _, err := s.ClaimRawPost(ctx, Claim{URL: "https://a.example/" + status, SourceID: base.SourceID})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := db.Exec(ctx, `UPDATE raw_posts SET status = $2, created_at = now() - interval '1 hour' WHERE id = $1`, p.ID, status); err != nil {
			t.Fatal(err)
		}
	}

	pending, err := s.ListPending(ctx, 20, 5*time.Minute)
	if err != nil || len(pending) != 0 {
		t.Errorf("pending %v, %v", pending, err)
	}

	unenriched, err := s.ListUnenriched(ctx, 20, 5*time.Minute, 10)
	if err != nil || len(unenriched) != 0 {
		t.Errorf("unenriched %v, %v", unenriched, err)
	}
}

func TestClaimRawPostSameLinkAgain(t *testing.T) {
	db, s, _ := rawPostDB(t)
	ctx := context.Background()

	var fromPost, link string
	if err := db.QueryRow(ctx, `
		INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content)
		SELECT id, 'https://a.example/from', 'sv', domain_id, 't', 's', 'c' FROM sources LIMIT 1 RETURNING id
		`).Scan(&fromPost); err != nil {
		t.Fatal(err)
	}

	if err := db.QueryRow(ctx, `INSERT INTO discovery_posts (url, from_post_id) VALUES ('https://b.example/x', $1) RETURNING id`, fromPost).Scan(&link); err != nil {
		t.Fatal(err)
	}

	c := Claim{URL: "https://b.example/x", DiscoveryPostID: link}

	first, _, err := s.ClaimRawPost(ctx, c)
	if err != nil {
		t.Fatal(err)
	}

	// The same link, retried by the catch-up after an error, claims its own pending row again.
	again, ok, err := s.ClaimRawPost(ctx, c)
	if err != nil || !ok || again.ID != first.ID {
		t.Errorf("again %v, %v, %+v", ok, err, again)
	}
}
