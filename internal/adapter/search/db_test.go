package discovery

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

func testDB(t *testing.T) *database.Database {
	return dbtest.New(t)
}

type seed struct {
	Trusted bool
	Content string
	BaseURL string
	URL     string
	Lang    string
	Age     time.Duration
}

func seedPost(t *testing.T, db *database.Database, s seed) domain.Post {
	t.Helper()

	ctx := context.Background()

	if s.BaseURL == "" {
		s.BaseURL = "https://own.example"
	}

	if s.URL == "" {
		s.URL = fmt.Sprintf("%s/%s", s.BaseURL, dbtest.RandomHex(t))
	}

	if s.Lang == "" {
		s.Lang = "sv"
	}

	p := domain.Post{URL: s.URL, Lang: s.Lang, Content: s.Content}

	if err := db.QueryRow(ctx, `
		INSERT INTO domains (base_url) VALUES ($1)
		ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url
		RETURNING id
		`, s.BaseURL).Scan(&p.DomainID); err != nil {
		t.Fatalf("err seed domain: %v", err)
	}

	if err := db.QueryRow(ctx, `
		INSERT INTO sources (domain_id, adapter, url, enabled) VALUES ($1, 'rss', $2, $3) RETURNING id
		`, p.DomainID, s.BaseURL+"/feed", s.Trusted).Scan(&p.SourceID); err != nil {
		t.Fatalf("err seed source: %v", err)
	}

	if err := db.QueryRow(ctx, `
		INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content, created_at)
		VALUES ($1, $2, $3, $4, 't', 's', $5, now() - $6::interval)
		RETURNING id, created_at
		`, p.SourceID, p.URL, p.Lang, p.DomainID, p.Content, s.Age).Scan(&p.ID, &p.CreatedAt); err != nil {
		t.Fatalf("err seed post: %v", err)
	}

	return p
}

func exec(t *testing.T, db *database.Database, sql string, args ...any) {
	t.Helper()

	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("err exec %q: %v", sql, err)
	}
}

func count(t *testing.T, db *database.Database, sql string, args ...any) (n int) {
	t.Helper()

	if err := db.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("err count %q: %v", sql, err)
	}

	return n
}
