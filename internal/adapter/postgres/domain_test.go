package postgres

import (
	"context"
	"testing"
)

func TestListPages(t *testing.T) {
	db, _, p := rawPostDB(t)
	ctx := context.Background()

	var other string
	if err := db.QueryRow(ctx, `INSERT INTO domains (base_url) VALUES ('https://b.example') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}

	for _, r := range []struct {
		url, domainID, page string
		age                 int
	}{
		{"https://a.example/old", p.DomainID, "<p>old</p>", 3},
		{"https://a.example/new", p.DomainID, "<p>new</p>", 1},
		{"https://a.example/mid", p.DomainID, "<p>mid</p>", 2},
		{"https://a.example/none", p.DomainID, "", 0},
		{"https://b.example/a", other, "<p>b</p>", 0},
	} {
		if _, err := db.Exec(ctx, `
			INSERT INTO raw_posts (url, domain_id, raw_content, created_at)
			VALUES ($1, $2, nullif($3, ''), now() - make_interval(hours => $4))
			`, r.url, r.domainID, r.page, r.age); err != nil {
			t.Fatal(err)
		}
	}

	s := NewStore(db)

	pages, err := s.ListPages(ctx, p.DomainID, 2)
	if err != nil {
		t.Fatal(err)
	}

	if len(pages) != 2 || pages[0].URL != "https://a.example/new" || pages[0].RawContent != "<p>new</p>" || pages[1].URL != "https://a.example/mid" {
		t.Errorf("pages %+v", pages)
	}
}
