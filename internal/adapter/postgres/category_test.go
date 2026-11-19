package postgres

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/jackc/pgx/v5"
)

func TestEnabledCategories(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	for _, q := range []string{
		`INSERT INTO tags (key) VALUES ('acquisition')`,
		`INSERT INTO tags (key, status) VALUES ('new_factory', 'proposed')`,
		`INSERT INTO tags (key, enabled) VALUES ('layoffs', false)`,
		`INSERT INTO categories (key) VALUES ('finance'), ('energy')`,
		`INSERT INTO categories (key, enabled) VALUES ('macro', false)`,
		`INSERT INTO category_terms (key, lang, term) VALUES ('finance', 'sv', 'finans'), ('finance', 'en', 'finance')`,
		`INSERT INTO category_tags (category, tag) VALUES ('finance', 'acquisition'), ('finance', 'new_factory'), ('finance', 'layoffs'), ('macro', 'acquisition')`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	s := NewCategoryStore(db)

	cats, err := s.EnabledCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(cats) != 2 || cats[0].Key != "energy" || cats[1].Key != "finance" {
		t.Fatalf("got %+v", cats)
	}

	energy, finance := cats[0], cats[1]

	if energy.Tags == nil || len(energy.Tags) != 0 || len(energy.Terms) != 0 {
		t.Errorf("energy %+v", energy)
	}

	if want := map[string]string{"sv": "finans", "en": "finance"}; !maps.Equal(finance.Terms, want) || !slices.Equal(finance.Tags, []string{"acquisition"}) {
		t.Errorf("finance %+v", finance)
	}
}

func TestListAdminCategories(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `
		INSERT INTO tags (key) VALUES ('acquisition');
		INSERT INTO tags (key, status) VALUES ('new_factory', 'proposed');
		INSERT INTO categories (key) VALUES ('finance');
		INSERT INTO categories (key, enabled) VALUES ('macro', false), ('energy', true);
		INSERT INTO category_terms (key, lang, term) VALUES ('finance', 'sv', 'finans'), ('finance', 'en', 'finance');
		INSERT INTO category_tags (category, tag) VALUES ('finance', 'acquisition'), ('finance', 'new_factory'), ('macro', 'acquisition');
		`); err != nil {
		t.Fatal(err)
	}

	domainID, sourceID := seedSource(t, db, "https://a.example", "https://a.example/feed")
	seedPost(t, db, sourceID, domainID, "https://a.example/1", "en", "One", []string{"acquisition"}, "0", "0")
	seedPost(t, db, sourceID, domainID, "https://a.example/2", "en", "Two", []string{"new_factory"}, "0", "0")
	seedPost(t, db, sourceID, domainID, "https://a.example/3", "en", "Three", []string{"other"}, "0", "0")

	s := NewCategoryStore(db)

	cats, err := s.ListAdminCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(cats) != 3 || cats[0].Key != "energy" || cats[1].Key != "finance" || cats[2].Key != "macro" {
		t.Fatalf("got %+v", cats)
	}

	energy, finance, macro := cats[0], cats[1], cats[2]

	if energy.TagCount != 0 || energy.PostCount != 0 || !energy.Enabled || energy.Tags == nil || len(energy.Terms) != 0 {
		t.Errorf("energy %+v", energy)
	}

	if want := map[string]string{"sv": "finans", "en": "finance"}; !maps.Equal(finance.Terms, want) || finance.TagCount != 2 || finance.PostCount != 2 {
		t.Errorf("finance %+v", finance)
	}

	if macro.Enabled || macro.TagCount != 1 || macro.PostCount != 1 {
		t.Errorf("macro %+v", macro)
	}
}

func TestCreateCategory(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	s := NewCategoryStore(db)

	if err := s.CreateCategory(ctx, domain.NewCategory{Key: "energy", Sv: "energi", En: "energy"}); err != nil {
		t.Fatal(err)
	}

	cats, err := s.ListAdminCategories(ctx)
	if err != nil || len(cats) != 1 || !cats[0].Enabled || !maps.Equal(cats[0].Terms, map[string]string{"sv": "energi", "en": "energy"}) {
		t.Fatalf("got %+v, %v", cats, err)
	}

	err = s.CreateCategory(ctx, domain.NewCategory{Key: "energy", Sv: "x", En: "x"})
	if !errors.Is(err, domain.ErrCategoryExists) {
		t.Errorf("duplicate: %v", err)
	}

	cats, err = s.ListAdminCategories(ctx)
	if err != nil || cats[0].Terms["en"] != "energy" {
		t.Errorf("duplicate changed terms: %+v, %v", cats, err)
	}
}

func TestSetCategoryEnabled(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	s := NewCategoryStore(db)

	if _, err := db.Exec(ctx, `INSERT INTO categories (key) VALUES ('energy')`); err != nil {
		t.Fatal(err)
	}

	if err := s.SetCategoryEnabled(ctx, "energy", false); err != nil {
		t.Fatal(err)
	}

	var enabled bool
	if err := db.QueryRow(ctx, `SELECT enabled FROM categories WHERE key = 'energy'`).Scan(&enabled); err != nil || enabled {
		t.Errorf("enabled %v, %v", enabled, err)
	}

	if err := s.SetCategoryEnabled(ctx, "nope", true); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown: %v", err)
	}
}

func TestSetTagCategories(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	s := NewCategoryStore(db)

	if _, err := db.Exec(ctx, `
		INSERT INTO tags (key) VALUES ('acquisition');
		INSERT INTO categories (key) VALUES ('finance'), ('deals');
		INSERT INTO categories (key, enabled) VALUES ('macro', false);
		INSERT INTO category_tags (category, tag) VALUES ('finance', 'acquisition');
		`); err != nil {
		t.Fatal(err)
	}

	got := func() []string {
		t.Helper()

		rows, err := db.Query(ctx, `SELECT category FROM category_tags WHERE tag = 'acquisition' ORDER BY category`)
		if err != nil {
			t.Fatal(err)
		}

		keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}

		return keys
	}

	if err := s.SetTagCategories(ctx, "acquisition", []string{"deals", "macro", "deals"}); err != nil {
		t.Fatal(err)
	}

	if keys := got(); !slices.Equal(keys, []string{"deals", "macro"}) {
		t.Errorf("replaced: %v", keys)
	}

	if err := s.SetTagCategories(ctx, "acquisition", []string{"finance", "nope"}); !errors.Is(err, domain.ErrUnknownCategory) {
		t.Errorf("unknown category: %v", err)
	}

	if keys := got(); !slices.Equal(keys, []string{"deals", "macro"}) {
		t.Errorf("after unknown category: %v", keys)
	}

	if err := s.SetTagCategories(ctx, "nope", []string{"finance"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown tag: %v", err)
	}

	if err := s.SetTagCategories(ctx, "acquisition", []string{}); err != nil {
		t.Fatal(err)
	}

	if keys := got(); len(keys) != 0 {
		t.Errorf("cleared: %v", keys)
	}
}
