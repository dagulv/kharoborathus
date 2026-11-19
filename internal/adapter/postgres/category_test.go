package postgres

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
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
