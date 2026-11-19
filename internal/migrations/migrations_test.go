package migrations_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/migrations"
	"github.com/jackc/pgx/v5"
)

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func count(t *testing.T, db querier, q string, args ...any) int {
	t.Helper()

	var n int
	if err := db.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("err count %q: %v", q, err)
	}

	return n
}

func TestSeed(t *testing.T) {
	ctx := context.Background()
	db := dbtest.Seeded(t)

	if n := count(t, db, `SELECT count(*) FROM categories WHERE enabled`); n != 18 {
		t.Errorf("enabled categories = %d, want 18", n)
	}

	if n := count(t, db, `SELECT count(*) FROM categories`); n != 18 {
		t.Errorf("categories = %d, want 18", n)
	}

	if n := count(t, db, `SELECT count(*) FROM categories c WHERE (SELECT count(*) FROM category_terms t WHERE t.key = c.key AND t.lang = 'sv') <> 1 OR (SELECT count(*) FROM category_terms t WHERE t.key = c.key AND t.lang = 'en') <> 1`); n != 0 {
		t.Errorf("categories without exactly one sv and en term = %d, want 0", n)
	}

	if n := count(t, db, `SELECT count(*) FROM tags WHERE status = 'active' AND enabled AND type = 'quality'`); n != 137 {
		t.Errorf("active enabled quality tags = %d, want 137", n)
	}

	if n := count(t, db, `SELECT count(*) FROM tags`); n != 137 {
		t.Errorf("tags = %d, want 137", n)
	}

	if n := count(t, db, `SELECT count(*) FROM tags k WHERE (SELECT count(*) FROM tag_terms t WHERE t.key = k.key AND t.lang = 'sv') <> 1 OR (SELECT count(*) FROM tag_terms t WHERE t.key = k.key AND t.lang = 'en') <> 1`); n != 0 {
		t.Errorf("tags without exactly one sv and en term = %d, want 0", n)
	}

	if n := count(t, db, `SELECT count(*) FROM tags k WHERE NOT EXISTS (SELECT 1 FROM category_tags ct WHERE ct.tag = k.key)`); n != 0 {
		t.Errorf("tags without category = %d, want 0", n)
	}

	rows, err := db.Query(ctx, `SELECT category FROM category_tags WHERE tag = 'batteries' ORDER BY category`)
	if err != nil {
		t.Fatalf("err query batteries: %v", err)
	}

	batteries, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("err scan batteries: %v", err)
	}

	if got := len(batteries); got != 3 || batteries[0] != "automotive" || batteries[1] != "energy" || batteries[2] != "industry" {
		t.Errorf("batteries categories = %v, want [automotive energy industry]", batteries)
	}

	var term string
	if err := db.QueryRow(ctx, `SELECT term FROM category_terms WHERE key = 'healthcare' AND lang = 'sv'`).Scan(&term); err != nil {
		t.Fatalf("err get healthcare term: %v", err)
	}

	if term != "hälsa och life science" {
		t.Errorf("healthcare sv = %q", term)
	}
}

func TestSeedExistingTags(t *testing.T) {
	ctx := context.Background()
	connString := dbtest.Empty(t)
	l := slog.New(slog.DiscardHandler)

	if err := migrations.MigrateTo(ctx, connString, l, 2); err != nil {
		t.Fatalf("err migrate to 2: %v", err)
	}

	conn, err := pgx.Connect(ctx, connString)
	if err != nil {
		t.Fatalf("err connect: %v", err)
	}
	defer conn.Close(ctx)

	for _, q := range []string{
		`INSERT INTO tags (key, status, enabled) VALUES ('dividend', 'proposed', false)`,
		`INSERT INTO tags (key, status) VALUES ('acquisition_deal', 'proposed')`,
		`INSERT INTO tag_aliases (alias, key) VALUES ('acquisition', 'acquisition_deal')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatalf("err exec %q: %v", q, err)
		}
	}

	if err := migrations.MigrateTo(ctx, connString, l, 3); err != nil {
		t.Fatalf("err migrate to 3: %v", err)
	}

	if n := count(t, conn, `SELECT count(*) FROM tags WHERE key = 'dividend' AND status = 'active' AND NOT enabled`); n != 1 {
		t.Errorf("dividend not active and disabled")
	}

	if n := count(t, conn, `SELECT count(*) FROM tags WHERE key = 'acquisition'`); n != 0 {
		t.Errorf("alias acquisition became a tag")
	}

	if n := count(t, conn, `SELECT count(*) FROM tags WHERE key = 'acquisition_deal' AND status = 'active'`); n != 1 {
		t.Errorf("acquisition_deal not active")
	}

	if n := count(t, conn, `SELECT count(*) FROM category_tags WHERE tag = 'acquisition_deal' AND category = 'deals'`); n != 1 {
		t.Errorf("acquisition_deal not in deals")
	}

	if n := count(t, conn, `SELECT count(*) FROM tag_terms WHERE key = 'acquisition_deal' AND lang = 'sv' AND term = 'förvärv'`); n != 1 {
		t.Errorf("acquisition_deal sv term missing")
	}
}

func TestEntitiesBackfill(t *testing.T) {
	cases := map[string]struct {
		volvoPosts int
		volvo      string
	}{
		"two posts below the rule": {2, "proposed"},
		"three trusted domains":    {3, "active"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			connString := dbtest.Empty(t)
			l := slog.New(slog.DiscardHandler)

			if err := migrations.MigrateTo(ctx, connString, l, 3); err != nil {
				t.Fatalf("err migrate to 3: %v", err)
			}

			conn, err := pgx.Connect(ctx, connString)
			if err != nil {
				t.Fatalf("err connect: %v", err)
			}
			defer conn.Close(ctx)

			posts := make([]string, 3)
			for i := range posts {
				base := fmt.Sprintf("https://d%d.example", i)

				var domainID, sourceID string
				if err := conn.QueryRow(ctx, `INSERT INTO domains (base_url) VALUES ($1) RETURNING id`, base).Scan(&domainID); err != nil {
					t.Fatalf("err insert domain: %v", err)
				}

				if err := conn.QueryRow(ctx, `INSERT INTO sources (domain_id, adapter, url, enabled) VALUES ($1, 'rss', $2, true) RETURNING id`, domainID, base+"/feed").Scan(&sourceID); err != nil {
					t.Fatalf("err insert source: %v", err)
				}

				if err := conn.QueryRow(ctx, `
					INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content)
					VALUES ($1, $2, 'sv', $3, 't', 's', 'c') RETURNING id
					`, sourceID, base+"/a", domainID).Scan(&posts[i]); err != nil {
					t.Fatalf("err insert post: %v", err)
				}
			}

			var volvo, ericsson string
			for _, q := range []struct {
				id     *string
				entity string
				query  string
			}{
				{&volvo, "volvo_cars", "Volvo Cars köper Northvolt"},
				{&ericsson, "ericsson", "Ericsson säljer enhet"},
			} {
				if err := conn.QueryRow(ctx, `INSERT INTO discovery_search_queries (normalized_entity, action, query) VALUES ($1, 'acquisition', $2) RETURNING id`, q.entity, q.query).Scan(q.id); err != nil {
					t.Fatalf("err insert query: %v", err)
				}
			}

			for i, post := range posts[:c.volvoPosts] {
				if _, err := conn.Exec(ctx, `INSERT INTO discovery_search_query_posts (query_id, post_id) VALUES ($1, $2)`, volvo, post); err != nil {
					t.Fatalf("err link volvo post %d: %v", i, err)
				}
			}

			if _, err := conn.Exec(ctx, `INSERT INTO discovery_search_query_posts (query_id, post_id) VALUES ($1, $2)`, ericsson, posts[0]); err != nil {
				t.Fatalf("err link ericsson post: %v", err)
			}

			if err := migrations.MigrateTo(ctx, connString, l, 4); err != nil {
				t.Fatalf("err migrate to 4: %v", err)
			}

			if n := count(t, conn, `SELECT count(*) FROM entities`); n != 2 {
				t.Errorf("entities = %d, want 2", n)
			}

			if n := count(t, conn, `SELECT count(*) FROM entities WHERE key = 'volvo_cars' AND name = 'Volvo Cars' AND kind = 'company' AND status = $1 AND enabled AND NOT focus`, c.volvo); n != 1 {
				t.Errorf("volvo_cars not company named Volvo Cars with status %s", c.volvo)
			}

			if n := count(t, conn, `SELECT count(*) FROM entities WHERE key = 'ericsson' AND name = 'Ericsson' AND status = 'proposed'`); n != 1 {
				t.Errorf("ericsson not proposed")
			}

			if n := count(t, conn, `SELECT count(*) FROM post_entities WHERE entity = 'volvo_cars'`); n != c.volvoPosts {
				t.Errorf("volvo_cars post_entities = %d, want %d", n, c.volvoPosts)
			}

			if n := count(t, conn, `SELECT count(*) FROM post_entities pe JOIN posts p ON p.id = pe.post_id WHERE pe.created_at = p.created_at`); n != c.volvoPosts+1 {
				t.Errorf("post_entities with post created_at = %d, want %d", n, c.volvoPosts+1)
			}

			if n := count(t, conn, `SELECT count(*) FROM entity_terms WHERE key = 'volvo_cars' AND lang = 'sv' AND term = 'Volvo Cars'`); n != 1 {
				t.Errorf("volvo_cars sv term missing")
			}

			if n := count(t, conn, `SELECT count(*) FROM entity_terms`); n != 2 {
				t.Errorf("entity_terms = %d, want 2", n)
			}
		})
	}
}
