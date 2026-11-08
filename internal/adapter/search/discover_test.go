package discovery

import (
	"context"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const volvoArticle = "Volvo Cars köper Northvolt. Saab köper Kockums. [l](https://news.example/a)"

var volvoTakeover = Keyword{Entity: "Volvo Cars", Action: "takeover", Query: "Volvo Cars köper Northvolt"}

func TestResolveAction(t *testing.T) {
	quality := map[string]bool{"acquisition": true}
	aliases := map[string]string{"takeover": "acquisition"}

	cases := map[string]struct {
		want string
		ok   bool
	}{
		"Takeover":    {"acquisition", true},
		"acquisition": {"acquisition", true},
		"new_factory": {"", false},
	}

	for in, c := range cases {
		if got, ok := resolveAction(in, quality, aliases); got != c.want || ok != c.ok {
			t.Errorf("%q: got %q, %v", in, got, ok)
		}
	}
}

func TestNormalizedEntity(t *testing.T) {
	cases := map[string]string{
		"Volvo  Cars":  "volvo_cars",
		"Ålandsbanken": "ålandsbanken",
	}

	for in, want := range cases {
		if got := normalizedEntity(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}

func discoverDB(t *testing.T) *database.Database {
	db := testDB(t)

	exec(t, db, `INSERT INTO tags (key) VALUES ('acquisition')`)
	exec(t, db, `INSERT INTO tag_aliases (alias, key) VALUES ('takeover', 'acquisition')`)

	return db
}

func discover(t *testing.T, db *database.Database, post domain.Post, events ...Keyword) (ids []string) {
	t.Helper()

	if err := pgx.BeginFunc(context.Background(), db, func(tx pgx.Tx) (err error) {
		ids, err = Discover(context.Background(), tx, post, events)
		return err
	}); err != nil {
		t.Fatalf("err discover: %v", err)
	}

	return ids
}

func TestDiscoverNewQuery(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	ids := discover(t, db, post, volvoTakeover)
	if len(ids) != 1 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE id = $1 AND normalized_entity = 'volvo_cars' AND action = 'acquisition' AND query = $2`, ids[0], volvoTakeover.Query); n != 1 {
		t.Errorf("query rows %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE query_id = $1 AND post_id = $2`, ids[0], post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_posts WHERE url = 'https://news.example/a' AND from_post_id = $1 AND discovery_search_query_id IS NULL`, post.ID); n != 1 {
		t.Errorf("discovery posts %d", n)
	}
}

func TestDiscoverSameEvent(t *testing.T) {
	db := discoverDB(t)
	first := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), volvoTakeover)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	if ids := discover(t, db, post, Keyword{Entity: "Volvo Cars", Action: "acquisition", Query: "Volvo Cars förvärvar Northvolt"}); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 1 {
		t.Errorf("query rows %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE query_id = $1 AND post_id = $2`, first[0], post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}
}

func TestDiscoverOldEvent(t *testing.T) {
	db := discoverDB(t)
	discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), volvoTakeover)
	exec(t, db, `UPDATE discovery_search_queries SET created_at = now() - interval '8 days'`)

	ids := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), Keyword{Entity: "Volvo Cars", Action: "acquisition", Query: "Volvo Cars förvärvar Northvolt"})
	if len(ids) != 1 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 2 {
		t.Errorf("query rows %d", n)
	}
}

func TestDiscoverQueryConflict(t *testing.T) {
	db := discoverDB(t)
	first := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), volvoTakeover)
	exec(t, db, `UPDATE discovery_search_queries SET created_at = now() - interval '8 days'`)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	if ids := discover(t, db, post, volvoTakeover); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE query_id = $1 AND post_id = $2`, first[0], post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}
}

func TestDiscoverSameEventTwice(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	ids := discover(t, db, post, volvoTakeover, Keyword{Entity: "Volvo Cars", Action: "acquisition", Query: "Volvo Cars förvärvar Northvolt"})
	if len(ids) != 1 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 1 {
		t.Errorf("query rows %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE post_id = $1`, post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}
}

func TestDiscoverUntrusted(t *testing.T) {
	db := discoverDB(t)
	first := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), volvoTakeover)
	post := seedPost(t, db, seed{Content: volvoArticle})

	if ids := discover(t, db, post, volvoTakeover, Keyword{Entity: "Saab", Action: "acquisition", Query: "Saab köper Kockums"}); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 1 {
		t.Errorf("query rows %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE query_id = $1 AND post_id = $2`, first[0], post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_posts WHERE from_post_id = $1`, post.ID); n != 0 {
		t.Errorf("discovery posts %d", n)
	}
}

func TestDiscoverNotQuality(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	if ids := discover(t, db, post, Keyword{Entity: "Volvo Cars", Action: "new_factory", Query: "Volvo Cars fabrik"}); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 0 {
		t.Errorf("query rows %d", n)
	}
}

func TestDiscoverLinkAlreadyPost(t *testing.T) {
	db := discoverDB(t)
	seedPost(t, db, seed{Trusted: true, BaseURL: "https://news.example", URL: "https://news.example/a"})
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	discover(t, db, post)

	if n := count(t, db, `SELECT count(*) FROM discovery_posts`); n != 0 {
		t.Errorf("discovery posts %d", n)
	}
}
