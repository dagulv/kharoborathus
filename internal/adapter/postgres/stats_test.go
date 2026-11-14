package postgres

import (
	"context"
	"maps"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

func TestStats(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	domainID, live := seedSource(t, db, "https://a.example", "https://a.example/feed")
	_, deleted := seedSource(t, db, "https://a.example", "https://a.example/old")

	if _, err := db.Exec(ctx, `UPDATE sources SET enabled = true, status = 'flagged' WHERE id = $1`, live); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(ctx, `UPDATE sources SET deleted_at = now() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}

	post := seedPost(t, db, live, domainID, "https://a.example/1", "en", "One", nil, "0", "0")
	seedPost(t, db, live, domainID, "https://a.example/2", "en", "Two", nil, "2 days", "2 days")

	if _, err := db.Exec(ctx, `
		INSERT INTO discovery_posts (url, from_post_id, status) VALUES ('https://b.example/1', $1, 'pending'), ('https://b.example/2', $1, 'accepted');
		`, post); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO discovery_search_queries (normalized_entity, action, query, searched_at, result)
		VALUES ('e', 'a', 'q1', now(), '{}'), ('e', 'a', 'q2', now(), NULL), ('e', 'a', 'q3', now() - interval '2 days', '{}')
		`); err != nil {
		t.Fatal(err)
	}

	s := NewStatsStore(db)

	got, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if got.Sources != 1 || got.EnabledSources != 1 || got.FlaggedSources != 1 || got.Posts != 2 || got.PostsToday != 1 || got.SearchesToday != 1 {
		t.Errorf("got %+v", got)
	}

	if !maps.Equal(got.Links, map[string]int{"pending": 1, "accepted": 1}) {
		t.Errorf("links %v", got.Links)
	}
}
