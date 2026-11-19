package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const volvoArticle = "Volvo Cars köper Northvolt. Saab köper Kockums. [l](https://news.example/a)"

var volvoTakeover = Keyword{Entity: "Volvo Cars", Action: "takeover", Query: "Volvo Cars köper Northvolt"}

var (
	volvoLinked = linkedEntity("volvo_cars", domain.StatusActive, "volvo cars")
	saabLinked  = linkedEntity("saab", domain.StatusActive, "saab")
)

func linkedEntity(key, status string, names ...string) domain.LinkedEntity {
	return domain.LinkedEntity{Key: key, Status: status, Names: names}
}

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

func discoverDB(t *testing.T) *database.Database {
	db := testDB(t)

	exec(t, db, `INSERT INTO tags (key) VALUES ('acquisition')`)
	exec(t, db, `INSERT INTO tag_aliases (alias, key) VALUES ('takeover', 'acquisition')`)

	return db
}

func discover(t *testing.T, db *database.Database, post domain.Post, linked []domain.LinkedEntity, events ...Keyword) []string {
	return discoverAll(t, db, post, linked, events...).Queries
}

func discoverAll(t *testing.T, db *database.Database, post domain.Post, linked []domain.LinkedEntity, events ...Keyword) (f Found) {
	t.Helper()

	if err := pgx.BeginFunc(context.Background(), db, func(tx pgx.Tx) (err error) {
		f, err = Discover(context.Background(), tx, post, events, linked)
		return err
	}); err != nil {
		t.Fatalf("err discover: %v", err)
	}

	return f
}

func TestDiscoverNewQuery(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	f := discoverAll(t, db, post, []domain.LinkedEntity{volvoLinked}, volvoTakeover)
	ids := f.Queries
	if len(ids) != 1 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE id = $1 AND normalized_entity = 'volvo_cars' AND action = 'acquisition' AND query = $2`, ids[0], volvoTakeover.Query); n != 1 {
		t.Errorf("query rows %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE query_id = $1 AND post_id = $2`, ids[0], post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}

	if len(f.Links) != 1 {
		t.Fatalf("got links %v", f.Links)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_posts WHERE id = $1 AND url = 'https://news.example/a' AND from_post_id = $2 AND discovery_search_query_id IS NULL`, f.Links[0], post.ID); n != 1 {
		t.Errorf("discovery posts %d", n)
	}
}

func TestDiscoverSameEvent(t *testing.T) {
	db := discoverDB(t)
	first := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), []domain.LinkedEntity{volvoLinked}, volvoTakeover)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	if ids := discover(t, db, post, []domain.LinkedEntity{volvoLinked}, Keyword{Entity: "Volvo Cars", Action: "acquisition", Query: "Volvo Cars förvärvar Northvolt"}); len(ids) != 0 {
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
	discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), []domain.LinkedEntity{volvoLinked}, volvoTakeover)
	exec(t, db, `UPDATE discovery_search_queries SET created_at = now() - interval '8 days'`)

	ids := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), []domain.LinkedEntity{volvoLinked}, Keyword{Entity: "Volvo Cars", Action: "acquisition", Query: "Volvo Cars förvärvar Northvolt"})
	if len(ids) != 1 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 2 {
		t.Errorf("query rows %d", n)
	}
}

func TestDiscoverQueryConflict(t *testing.T) {
	db := discoverDB(t)
	first := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), []domain.LinkedEntity{volvoLinked}, volvoTakeover)
	exec(t, db, `UPDATE discovery_search_queries SET created_at = now() - interval '8 days'`)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	if ids := discover(t, db, post, []domain.LinkedEntity{volvoLinked}, volvoTakeover); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE query_id = $1 AND post_id = $2`, first[0], post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}
}

func TestDiscoverSameEventTwice(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	ids := discover(t, db, post, []domain.LinkedEntity{volvoLinked}, volvoTakeover, Keyword{Entity: "Volvo Cars", Action: "acquisition", Query: "Volvo Cars förvärvar Northvolt"})
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
	first := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), []domain.LinkedEntity{volvoLinked}, volvoTakeover)
	post := seedPost(t, db, seed{Content: volvoArticle})

	if ids := discover(t, db, post, []domain.LinkedEntity{volvoLinked, saabLinked}, volvoTakeover, Keyword{Entity: "Saab", Action: "acquisition", Query: "Saab köper Kockums"}); len(ids) != 0 {
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

	if ids := discover(t, db, post, []domain.LinkedEntity{volvoLinked}, Keyword{Entity: "Volvo Cars", Action: "new_factory", Query: "Volvo Cars fabrik"}); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 0 {
		t.Errorf("query rows %d", n)
	}
}

func TestDiscoverUnlinkedEntity(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	if ids := discover(t, db, post, []domain.LinkedEntity{saabLinked}, volvoTakeover); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 0 {
		t.Errorf("query rows %d", n)
	}
}

func TestDiscoverReportsUnmatchedEvent(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	f := discoverAll(t, db, post, []domain.LinkedEntity{saabLinked}, volvoTakeover)
	if len(f.Unmatched) != 1 || f.Unmatched[0] != volvoTakeover {
		t.Errorf("unmatched %v", f.Unmatched)
	}

	f = discoverAll(t, db, post, []domain.LinkedEntity{volvoLinked}, volvoTakeover)
	if len(f.Unmatched) != 0 {
		t.Errorf("unmatched %v", f.Unmatched)
	}
}

func TestDiscoverProposedEntity(t *testing.T) {
	db := discoverDB(t)
	proposed := []domain.LinkedEntity{linkedEntity("volvo_cars", domain.StatusProposed, "volvo cars")}
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	if ids := discover(t, db, post, proposed, volvoTakeover); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 0 {
		t.Errorf("query rows %d", n)
	}

	first := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), []domain.LinkedEntity{volvoLinked}, volvoTakeover)
	post = seedPost(t, db, seed{Content: volvoArticle})

	if ids := discover(t, db, post, proposed, volvoTakeover); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE query_id = $1 AND post_id = $2`, first[0], post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}
}

func TestDiscoverFocusedProposed(t *testing.T) {
	db := discoverDB(t)
	focused := []domain.LinkedEntity{{Key: "volvo_cars", Status: domain.StatusProposed, Focus: true, Names: []string{"volvo cars"}, Term: "Volvo Cars"}}
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	if ids := discover(t, db, post, focused, volvoTakeover); len(ids) != 2 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE action = 'acquisition'`); n != 1 {
		t.Errorf("event rows %d", n)
	}
}

func TestDiscoverEventByAliasAndTerm(t *testing.T) {
	db := discoverDB(t)
	linked := []domain.LinkedEntity{{
		Key:     "volvo_cars",
		Status:  domain.StatusActive,
		Names:   []string{"volvo cars", "volvo personvagnar"},
		Aliases: []string{"volvo_car_corporation"},
	}}

	cases := map[string]string{
		"alias": "Volvo Car Corporation",
		"term":  "Volvo Personvagnar",
	}

	for name, entity := range cases {
		exec(t, db, `DELETE FROM discovery_search_queries`)
		post := seedPost(t, db, seed{Trusted: true, Content: entity + " köper Northvolt."})

		if ids := discover(t, db, post, linked, Keyword{Entity: entity, Action: "acquisition", Query: entity + " köper Northvolt"}); len(ids) != 1 {
			t.Fatalf("%s: got ids %v", name, ids)
		}

		if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE normalized_entity = 'volvo_cars'`); n != 1 {
			t.Errorf("%s: query rows %d", name, n)
		}
	}
}

func TestDiscoverLinkAlreadyPost(t *testing.T) {
	db := discoverDB(t)
	seedPost(t, db, seed{Trusted: true, BaseURL: "https://news.example", URL: "https://news.example/a"})
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	discover(t, db, post, nil)

	if n := count(t, db, `SELECT count(*) FROM discovery_posts`); n != 0 {
		t.Errorf("discovery posts %d", n)
	}
}

func TestDiscoverConcurrent(t *testing.T) {
	db := discoverDB(t)
	ctx := context.Background()

	txA, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback(ctx)

	foundA, err := Discover(ctx, txA, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), []Keyword{volvoTakeover}, []domain.LinkedEntity{volvoLinked})
	if err != nil || len(foundA.Queries) != 1 {
		t.Fatalf("a: ids %v, %v", foundA.Queries, err)
	}

	type result struct {
		ids []string
		err error
	}

	done := make(chan result)
	// No shared link, or the discovery_posts unique index would serialize the two transactions on its own.
	post := seedPost(t, db, seed{Trusted: true, Content: "Volvo Cars köper Northvolt."})

	go func() {
		var r result
		r.err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) (err error) {
			f, err := Discover(ctx, tx, post, []Keyword{{Entity: "Volvo Cars", Action: "acquisition", Query: "Volvo Cars förvärvar Northvolt"}}, []domain.LinkedEntity{volvoLinked})
			r.ids = f.Queries
			return err
		})
		done <- r
	}()

	time.Sleep(200 * time.Millisecond)

	if err := txA.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if r := <-done; r.err != nil || len(r.ids) != 0 {
		t.Fatalf("b: ids %v, %v", r.ids, r.err)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 1 {
		t.Errorf("query rows %d", n)
	}
}

func TestOld(t *testing.T) {
	stale := time.Now().AddDate(0, 0, -dedupeWindow-1)
	fresh := time.Now().AddDate(0, 0, -1)

	cases := []struct {
		postedAt *time.Time
		want     bool
	}{
		{nil, false},
		{&fresh, false},
		{&stale, true},
	}

	for _, c := range cases {
		if got := old(domain.Post{PostedAt: c.postedAt}); got != c.want {
			t.Errorf("posted %v: old %v", c.postedAt, got)
		}
	}
}

func TestDiscoverOldPost(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})
	stale := time.Now().AddDate(0, 0, -8)
	post.PostedAt = &stale

	f := discoverAll(t, db, post, []domain.LinkedEntity{volvoLinked}, volvoTakeover)
	if len(f.Queries) != 0 {
		t.Fatalf("got ids %v", f.Queries)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 0 {
		t.Errorf("query rows %d", n)
	}

	if len(f.Links) != 1 {
		t.Errorf("got links %v", f.Links)
	}
}

func TestDiscoverOldPostJoinsEvent(t *testing.T) {
	db := discoverDB(t)
	first := discover(t, db, seedPost(t, db, seed{Trusted: true, Content: volvoArticle}), []domain.LinkedEntity{volvoLinked}, volvoTakeover)

	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})
	stale := time.Now().AddDate(0, 0, -8)
	post.PostedAt = &stale

	if ids := discover(t, db, post, []domain.LinkedEntity{volvoLinked}, volvoTakeover); len(ids) != 0 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 1 {
		t.Errorf("query rows %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE query_id = $1 AND post_id = $2`, first[0], post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}
}

func TestDiscoverUndatedPost(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})
	post.PostedAt = nil

	if ids := discover(t, db, post, []domain.LinkedEntity{volvoLinked}, volvoTakeover); len(ids) != 1 {
		t.Fatalf("got ids %v", ids)
	}
}

func TestDepthQueryTextNotUnique(t *testing.T) {
	db := discoverDB(t)

	for range 2 {
		exec(t, db, `INSERT INTO discovery_search_queries (normalized_entity, action, query) VALUES ('volvo_cars', 'depth', 'Volvo Cars')`)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE action = 'depth'`); n != 2 {
		t.Errorf("depth rows %d", n)
	}

	exec(t, db, `INSERT INTO discovery_search_queries (normalized_entity, action, query) VALUES ('volvo_cars', 'acquisition', 'Volvo Cars köper')`)

	if _, err := db.Exec(context.Background(), `INSERT INTO discovery_search_queries (normalized_entity, action, query) VALUES ('volvo_cars', 'acquisition', 'Volvo Cars köper')`); err == nil {
		t.Error("duplicate acquisition query inserted")
	}
}

func TestInsertQueryConflictSkipsDepth(t *testing.T) {
	db := discoverDB(t)
	exec(t, db, `INSERT INTO discovery_search_queries (normalized_entity, action, query) VALUES ('volvo_cars', 'depth', 'Volvo Cars köper')`)

	var want string
	if err := db.QueryRow(context.Background(), `INSERT INTO discovery_search_queries (normalized_entity, action, query) VALUES ('volvo_cars', 'acquisition', 'Volvo Cars köper') RETURNING id`).Scan(&want); err != nil {
		t.Fatalf("err seed acquisition: %v", err)
	}

	var id string
	var created bool
	if err := pgx.BeginFunc(context.Background(), db, func(tx pgx.Tx) (err error) {
		id, created, err = insertQuery(context.Background(), tx, "volvo_cars", "acquisition", "Volvo Cars köper")
		return err
	}); err != nil {
		t.Fatalf("err insert query: %v", err)
	}

	if created || id != want {
		t.Errorf("got %s created %v, want existing acquisition %s", id, created, want)
	}
}

func TestMatchEntityPriority(t *testing.T) {
	byTerm := linkedEntity("volvo_group", domain.StatusActive, "volvo cars")
	byKey := linkedEntity("volvo_cars", domain.StatusActive)
	byAlias := domain.LinkedEntity{Key: "other", Aliases: []string{"volvo_cars"}}

	cases := map[string]struct {
		linked []domain.LinkedEntity
		want   string
	}{
		"key beats earlier term":   {[]domain.LinkedEntity{byTerm, byKey}, "volvo_cars"},
		"key beats earlier alias":  {[]domain.LinkedEntity{byAlias, byKey}, "volvo_cars"},
		"alias beats earlier term": {[]domain.LinkedEntity{byTerm, byAlias}, "other"},
		"term when alone":          {[]domain.LinkedEntity{byTerm}, "volvo_group"},
	}

	for name, c := range cases {
		if got, ok := matchEntity("Volvo Cars", c.linked); !ok || got.Key != c.want {
			t.Errorf("%s: got %q, %v", name, got.Key, ok)
		}
	}
}

func focusedVolvo(term string) []domain.LinkedEntity {
	return []domain.LinkedEntity{{Key: "volvo_cars", Status: domain.StatusActive, Focus: true, Names: []string{"volvo cars"}, Term: term}}
}

func TestDiscoverDepth(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	ids := discover(t, db, post, focusedVolvo("Volvo Cars"))
	if len(ids) != 1 {
		t.Fatalf("got ids %v", ids)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE id = $1 AND normalized_entity = 'volvo_cars' AND action = 'depth' AND query = 'Volvo Cars'`, ids[0]); n != 1 {
		t.Errorf("query rows %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE query_id = $1 AND post_id = $2`, ids[0], post.ID); n != 1 {
		t.Errorf("query links %d", n)
	}

	second := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})

	if got := discover(t, db, second, focusedVolvo("Volvo Cars")); len(got) != 0 {
		t.Fatalf("second post got ids %v", got)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE action = 'depth'`); n != 1 {
		t.Errorf("depth rows %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_query_posts WHERE post_id = $1`, second.ID); n != 0 {
		t.Errorf("second post links %d", n)
	}
}

func TestDiscoverDepthSkips(t *testing.T) {
	db := discoverDB(t)

	cases := map[string]struct {
		post   seed
		linked []domain.LinkedEntity
	}{
		"untrusted":   {seed{Content: volvoArticle}, focusedVolvo("Volvo Cars")},
		"not focused": {seed{Trusted: true, Content: volvoArticle}, []domain.LinkedEntity{{Key: "volvo_cars", Status: domain.StatusActive, Term: "Volvo Cars"}}},
	}

	for name, c := range cases {
		if ids := discover(t, db, seedPost(t, db, c.post), c.linked); len(ids) != 0 {
			t.Errorf("%s: got ids %v", name, ids)
		}
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries`); n != 0 {
		t.Errorf("query rows %d", n)
	}
}

func TestQueueDepthSameTextAsEvent(t *testing.T) {
	db := discoverDB(t)
	post := seedPost(t, db, seed{Trusted: true, Content: volvoArticle})
	exec(t, db, `INSERT INTO discovery_search_queries (normalized_entity, action, query) VALUES ('volvo_cars', 'acquisition', 'Volvo Cars')`)

	var id string
	var created bool
	if err := pgx.BeginFunc(context.Background(), db, func(tx pgx.Tx) (err error) {
		id, created, err = QueueDepth(context.Background(), tx, "volvo_cars", post.ID, "Volvo Cars")
		return err
	}); err != nil {
		t.Fatalf("err queue depth: %v", err)
	}

	if !created {
		t.Fatal("not created")
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE id = $1 AND action = 'depth' AND query = 'Volvo Cars'`, id); n != 1 {
		t.Errorf("depth rows %d", n)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE query = 'Volvo Cars'`); n != 2 {
		t.Errorf("rows %d", n)
	}
}
