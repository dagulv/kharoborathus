package postgres

import (
	"context"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/jackc/pgx/v5"
)

func tagDB(t *testing.T) (*database.Database, TagStore) {
	db := dbtest.New(t)

	for _, q := range []string{
		`INSERT INTO tags (key) VALUES ('acquisition'), ('contract')`,
		`INSERT INTO tags (key, enabled) VALUES ('layoffs', false)`,
		`INSERT INTO tags (key, status) VALUES ('new_factory', 'proposed')`,
		`INSERT INTO tags (key, status, enabled) VALUES ('ipo', 'proposed', false)`,
		`INSERT INTO tag_terms (key, lang, term) VALUES ('acquisition', 'sv', 'förvärv'), ('acquisition', 'sv', 'uppköp'), ('acquisition', 'en', 'acquisition')`,
		`INSERT INTO tag_aliases (alias, key) VALUES ('takeover', 'acquisition')`,
	} {
		if _, err := db.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}

	return db, NewTagStore(db)
}

func TestTagKeys(t *testing.T) {
	_, s := tagDB(t)

	keys, err := s.TagKeys(context.Background())
	if want := []string{"acquisition", "contract", "ipo", "layoffs", "new_factory"}; err != nil || !slices.Equal(keys, want) {
		t.Errorf("got %v, %v", keys, err)
	}
}

func TestActiveTags(t *testing.T) {
	_, s := tagDB(t)

	tags, err := s.ActiveTags(context.Background(), "sv")
	if err != nil {
		t.Fatal(err)
	}

	if len(tags) != 2 || tags[0].Key != "acquisition" || tags[0].Type != "quality" || !slices.Equal(tags[0].Terms, []string{"förvärv", "uppköp"}) || tags[1].Key != "contract" || len(tags[1].Terms) != 0 {
		t.Errorf("got %+v", tags)
	}
}

func TestSaveProposals(t *testing.T) {
	db, s := tagDB(t)
	ctx := context.Background()

	save := func(lang string, ps ...Proposal) []string {
		var keys []string

		if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) (err error) {
			keys, err = s.SaveProposals(ctx, tx, lang, ps)
			return err
		}); err != nil {
			t.Fatal(err)
		}

		return keys
	}

	keys := save("sv",
		Proposal{Key: "share_buyback", Term: "återköp"},
		Proposal{Key: "new_factory", Term: "ny fabrik"},
		Proposal{Key: "acquisition", Term: "köp"},
		Proposal{Key: "takeover", Term: "övertagande"},
		Proposal{Key: "share_buyback", Term: "återköp"},
		Proposal{Key: "ipo", Term: "börsnotering"},
	)

	if want := []string{"share_buyback", "new_factory"}; !slices.Equal(keys, want) {
		t.Errorf("keys %v", keys)
	}

	count := func(q string, args ...any) (n int) {
		if err := db.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if n := count(`SELECT count(*) FROM tags WHERE key = 'share_buyback' AND status = 'proposed'`); n != 1 {
		t.Errorf("proposed tag rows %d", n)
	}

	if n := count(`SELECT count(*) FROM tag_terms WHERE (key, lang, term) IN (('share_buyback', 'sv', 'återköp'), ('new_factory', 'sv', 'ny fabrik'))`); n != 2 {
		t.Errorf("proposed terms %d", n)
	}

	if n := count(`SELECT count(*) FROM tag_terms WHERE key = 'acquisition'`); n != 3 {
		t.Errorf("active tag terms %d", n)
	}

	if n := count(`SELECT count(*) FROM tags WHERE key = 'takeover'`); n != 0 {
		t.Errorf("alias became a tag")
	}

	if keys := save("", Proposal{Key: "dividend", Term: "utdelning"}); !slices.Equal(keys, []string{"dividend"}) || count(`SELECT count(*) FROM tag_terms WHERE key = 'dividend'`) != 0 {
		t.Errorf("empty lang: keys %v", keys)
	}
}
