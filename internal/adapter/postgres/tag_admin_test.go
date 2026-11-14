package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/jackc/pgx/v5"
)

func TestTagOverview(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `
		INSERT INTO tags (key, status, enabled) VALUES ('buyback', 'active', true), ('newtag', 'proposed', true), ('sleepy', 'proposed', false);
		INSERT INTO tag_terms (key, lang, term) VALUES ('buyback', 'sv', 'återköp'), ('buyback', 'en', 'share buyback'), ('newtag', 'en', 'new thing');
		INSERT INTO tag_aliases (alias, key) VALUES ('buy-back', 'buyback');
		`); err != nil {
		t.Fatal(err)
	}

	domainA, sourceA := seedSource(t, db, "https://a.example", "https://a.example/feed")
	domainB, sourceB := seedSource(t, db, "https://b.example", "https://b.example/feed")
	seedPost(t, db, sourceA, domainA, "https://a.example/1", "en", "One", []string{"buyback", "newtag", "sleepy"}, "0", "0")
	seedPost(t, db, sourceB, domainB, "https://b.example/1", "en", "Two", []string{"buyback", "newtag"}, "0", "0")

	s := NewTagStore(db)

	got, err := s.TagOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Tags) != 3 || got.Tags[0].Key != "buyback" || got.Tags[0].PostCount != 2 || !slices.Equal(got.Tags[0].Terms, []string{"en: share buyback", "sv: återköp"}) {
		t.Errorf("tags: %+v", got.Tags)
	}

	if len(got.Tags[2].Terms) != 0 || got.Tags[2].Terms == nil {
		t.Errorf("tag without terms: %+v", got.Tags[2])
	}

	if len(got.Proposals) != 1 || got.Proposals[0].Key != "newtag" || got.Proposals[0].PostCount != 2 || got.Proposals[0].DomainCount != 2 || !slices.Equal(got.Proposals[0].Terms, []string{"en: new thing"}) {
		t.Errorf("proposals: %+v", got.Proposals)
	}

	if !slices.Equal(got.Aliases, []domain.TagAlias{{Alias: "buy-back", Key: "buyback"}}) {
		t.Errorf("aliases: %+v", got.Aliases)
	}
}

func TestTagOverviewEmpty(t *testing.T) {
	s := NewTagStore(dbtest.New(t))

	got, err := s.TagOverview(context.Background())
	if err != nil || got.Tags == nil || got.Proposals == nil || got.Aliases == nil {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestSetTagEnabled(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `INSERT INTO tags (key) VALUES ('a/b'), ('a b')`); err != nil {
		t.Fatal(err)
	}

	s := NewTagStore(db)
	if err := s.SetTagEnabled(ctx, "a/b", false); err != nil {
		t.Fatal(err)
	}

	var disabled []string
	rows, _ := db.Query(ctx, `SELECT key FROM tags WHERE NOT enabled`)
	disabled, _ = pgx.CollectRows(rows, pgx.RowTo[string])

	if !slices.Equal(disabled, []string{"a/b"}) {
		t.Errorf("disabled: %v", disabled)
	}
}

func TestMergeTag(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `
		INSERT INTO tags (key) VALUES ('from'), ('to');
		INSERT INTO tag_terms (key, lang, term) VALUES ('from', 'en', 'old term'), ('to', 'en', 'new term');
		INSERT INTO tag_aliases (alias, key) VALUES ('older', 'from');
		`); err != nil {
		t.Fatal(err)
	}

	domainID, sourceID := seedSource(t, db, "https://a.example", "https://a.example/feed")
	both := seedPost(t, db, sourceID, domainID, "https://a.example/1", "en", "One", []string{"from", "to"}, "0", "0")
	one := seedPost(t, db, sourceID, domainID, "https://a.example/2", "en", "Two", []string{"x", "from"}, "0", "0")

	s := NewTagStore(db)

	if err := s.MergeTag(ctx, "from", "from"); !errors.Is(err, domain.ErrMergeSelf) {
		t.Errorf("self: %v", err)
	}

	if err := s.MergeTag(ctx, "from", "missing"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown target: %v", err)
	}

	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM tags WHERE key = 'from'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("from gone after failed merge: %d, %v", n, err)
	}

	if err := s.MergeTag(ctx, "from", "to"); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string][]string{both: {"to"}, one: {"to", "x"}} {
		var tags []string
		if err := db.QueryRow(ctx, `SELECT tags FROM posts WHERE id = $1`, id).Scan(&tags); err != nil {
			t.Fatal(err)
		}

		slices.Sort(tags)
		if !slices.Equal(tags, want) {
			t.Errorf("post tags %v, want %v", tags, want)
		}
	}

	rows, _ := db.Query(ctx, `SELECT term FROM tag_terms WHERE key = 'to' ORDER BY term`)
	terms, _ := pgx.CollectRows(rows, pgx.RowTo[string])
	if !slices.Equal(terms, []string{"new term", "old term"}) {
		t.Errorf("terms %v", terms)
	}

	got, err := s.TagOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(got.Aliases, []domain.TagAlias{{Alias: "from", Key: "to"}, {Alias: "older", Key: "to"}}) {
		t.Errorf("aliases %+v", got.Aliases)
	}

	if len(got.Tags) != 1 || got.Tags[0].Key != "to" {
		t.Errorf("tags %+v", got.Tags)
	}
}
