package postgres

import (
	"context"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/database"
)

func seedSource(t *testing.T, db *database.Database, baseURL, url string) (domainID, sourceID string) {
	t.Helper()

	if err := db.QueryRow(context.Background(), `
		WITH d AS (INSERT INTO domains (base_url) VALUES ($1) ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url RETURNING id)
		INSERT INTO sources (domain_id, adapter, url) SELECT id, 'rss', $2 FROM d RETURNING domain_id, id
		`, baseURL, url).Scan(&domainID, &sourceID); err != nil {
		t.Fatal(err)
	}

	return domainID, sourceID
}

func seedPost(t *testing.T, db *database.Database, sourceID, domainID, url, lang, title string, tags []string, postedAgo, createdAgo string) string {
	t.Helper()

	if tags == nil {
		tags = []string{}
	}

	var id string
	if err := db.QueryRow(context.Background(), `
		INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, tags, posted_at, created_at)
		VALUES ($1, $2, $3, $4, $5, 'summary', 'body', $6, now() - $7::interval, now() - $8::interval)
		RETURNING id
		`, sourceID, domainID, url, lang, title, tags, postedAgo, createdAgo).Scan(&id); err != nil {
		t.Fatal(err)
	}

	seedRawPost(t, db, sourceID, domainID, url, "accepted", `{"title": "raw title"}`, createdAgo)

	return id
}

// An empty sourceID or content is stored as NULL.
func seedRawPost(t *testing.T, db *database.Database, sourceID, domainID, url, status, content, createdAgo string) string {
	t.Helper()

	var id string
	if err := db.QueryRow(context.Background(), `
		INSERT INTO raw_posts (source_id, domain_id, url, lang, status, content, created_at)
		VALUES (NULLIF($1, '')::uuid, $2, $3, 'sv', $4, NULLIF($5, '')::jsonb, now() - $6::interval)
		RETURNING id
		`, sourceID, domainID, url, status, content, createdAgo).Scan(&id); err != nil {
		t.Fatal(err)
	}

	return id
}
