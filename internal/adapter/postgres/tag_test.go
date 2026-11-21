package postgres

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
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
	if want := []string{"acquisition", "contract", "layoffs", "ipo", "new_factory"}; err != nil || !slices.Equal(keys, want) {
		t.Errorf("got %v, %v", keys, err)
	}
}

func TestClassifiableTags(t *testing.T) {
	db, s := tagDB(t)
	ctx := context.Background()

	for _, q := range []string{
		`INSERT INTO categories (key) VALUES ('deals')`,
		`INSERT INTO categories (key, enabled) VALUES ('macro', false)`,
		`INSERT INTO category_tags (category, tag) VALUES ('deals', 'acquisition'), ('macro', 'acquisition')`,
		`INSERT INTO tags (key, status, created_at) VALUES ('old_factory', 'proposed', now() - interval '40 days'), ('old_carried', 'proposed', now() - interval '40 days')`,
		`INSERT INTO domains (base_url) VALUES ('https://a.example')`,
		`INSERT INTO sources (domain_id, adapter, url) SELECT id, 'rss', 'https://a.example/feed' FROM domains`,
		`INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content, tags)
		SELECT s.id, 'https://a.example/1', 'sv', s.domain_id, 't', 's', 'c', '{old_carried}' FROM sources s`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	tags, err := s.ClassifiableTags(ctx, "sv")
	if err != nil {
		t.Fatal(err)
	}

	got := make(map[string]domain.Tag, len(tags))
	keys := make([]string, 0, len(tags))

	for _, tag := range tags {
		got[tag.Key] = tag
		keys = append(keys, tag.Key)
	}

	if want := []string{"acquisition", "contract", "new_factory", "old_carried"}; !slices.Equal(keys, want) {
		t.Fatalf("keys %v, want %v", keys, want)
	}

	acq := got["acquisition"]
	if acq.Status != domain.StatusActive || acq.Type != "quality" || !slices.Equal(acq.Terms, []string{"förvärv", "uppköp"}) || !slices.Equal(acq.Categories, []string{"deals"}) {
		t.Errorf("acquisition %+v", acq)
	}

	if c := got["contract"]; len(c.Terms) != 0 || c.Categories == nil || len(c.Categories) != 0 {
		t.Errorf("contract %+v", c)
	}

	if got["new_factory"].Status != domain.StatusProposed || got["old_carried"].Status != domain.StatusProposed {
		t.Errorf("proposed statuses %+v %+v", got["new_factory"], got["old_carried"])
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

func TestSaveProposalsConcurrent(t *testing.T) {
	db, s := tagDB(t)
	ctx := context.Background()

	txA, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback(ctx)

	if _, err := s.SaveProposals(ctx, txA, "sv", []Proposal{{Key: "dividend", Term: "utdelning"}}); err != nil {
		t.Fatal(err)
	}

	type result struct {
		keys []string
		err  error
	}

	done := make(chan result)

	go func() {
		var r result
		r.err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) (err error) {
			r.keys, err = s.SaveProposals(ctx, tx, "sv", []Proposal{{Key: "dividend", Term: "utdelning"}})
			return err
		})
		done <- r
	}()

	time.Sleep(200 * time.Millisecond)

	if err := txA.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if r := <-done; r.err != nil || !slices.Equal(r.keys, []string{"dividend"}) {
		t.Errorf("b: keys %v, %v", r.keys, r.err)
	}
}

func TestSaveProposalCategories(t *testing.T) {
	db, s := tagDB(t)
	ctx := context.Background()

	for _, q := range []string{
		`INSERT INTO categories (key) VALUES ('energy'), ('automotive')`,
		`INSERT INTO categories (key, enabled) VALUES ('macro', false)`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	save := func(p Proposal) {
		if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
			_, err := s.SaveProposals(ctx, tx, "sv", []Proposal{p})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	cats := func(tag string) []string {
		rows, err := db.Query(ctx, `SELECT category FROM category_tags WHERE tag = $1 ORDER BY category`, tag)
		if err != nil {
			t.Fatal(err)
		}

		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}

		return got
	}

	save(Proposal{Key: "solid_state_battery", Categories: []string{"energy", "automotive", "macro", "nope"}})

	if got := cats("solid_state_battery"); !slices.Equal(got, []string{"automotive", "energy"}) {
		t.Errorf("created: %v", got)
	}

	save(Proposal{Key: "solid_state_battery", Categories: []string{"macro"}})

	if got := cats("solid_state_battery"); !slices.Equal(got, []string{"automotive", "energy"}) {
		t.Errorf("second save: %v", got)
	}

	if _, err := db.Exec(ctx, `DELETE FROM category_tags WHERE tag = 'solid_state_battery'`); err != nil {
		t.Fatal(err)
	}

	save(Proposal{Key: "solid_state_battery", Categories: []string{"energy"}})

	if got := cats("solid_state_battery"); len(got) != 0 {
		t.Errorf("cleared: %v", got)
	}

	save(Proposal{Key: "new_factory", Categories: []string{"energy"}})
	save(Proposal{Key: "takeover", Categories: []string{"energy"}})

	if got := append(cats("new_factory"), cats("acquisition")...); len(got) != 0 {
		t.Errorf("existing tags: %v", got)
	}
}

func TestPromoteTags(t *testing.T) {
	db, s := tagDB(t)
	ctx := context.Background()

	n := 0

	post := func(tag string, site int, enabled bool, ageDays int) {
		t.Helper()

		n++

		if _, err := db.Exec(ctx, `
			WITH d AS (
				INSERT INTO domains (base_url) VALUES ($1) ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url RETURNING id
			), s AS (
				INSERT INTO sources (domain_id, adapter, url, enabled) SELECT id, 'rss', $1 || '/feed', $3 FROM d RETURNING id, domain_id
			)
			INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content, tags, created_at)
			SELECT id, $2, 'sv', domain_id, 't', 's', 'c', ARRAY[$4]::text[], now() - make_interval(days => $5) FROM s
			`, fmt.Sprintf("https://d%d.example", site), fmt.Sprintf("https://d%d.example/%d", site, n), enabled, tag, ageDays); err != nil {
			t.Fatal(err)
		}
	}

	for _, key := range []string{"promote_ok", "two_domains", "four_posts", "disabled_source", "old_post"} {
		if _, err := db.Exec(ctx, `INSERT INTO tags (key, status) VALUES ($1, 'proposed')`, key); err != nil {
			t.Fatal(err)
		}
	}

	for i := range 5 {
		post("promote_ok", i%3, true, 0)
		post("two_domains", i%2, true, 0)
		post("acquisition", i%3, true, 0)
		post("unknown", i%3, true, 0)
	}

	for i := range 4 {
		post("four_posts", i%3, true, 0)
		post("disabled_source", i%3, true, 0)
		post("old_post", i%3, true, 0)
	}

	post("disabled_source", 2, false, 0)
	post("old_post", 2, true, 31)

	var promoted []string

	if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) (err error) {
		promoted, err = s.PromoteTags(ctx, tx, []string{"two_domains", "promote_ok", "four_posts", "disabled_source", "old_post", "acquisition", "unknown", "promote_ok"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if want := []string{"promote_ok"}; !slices.Equal(promoted, want) {
		t.Errorf("promoted %v, want %v", promoted, want)
	}

	rows, err := db.Query(ctx, `SELECT key FROM tags WHERE status = 'proposed' ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}

	left, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"disabled_source", "four_posts", "ipo", "new_factory", "old_post", "two_domains"}; !slices.Equal(left, want) {
		t.Errorf("still proposed %v, want %v", left, want)
	}
}
