package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/lib/query"
	"github.com/jackc/pgx/v5"
)

type entityFixture struct {
	t         *testing.T
	db        *database.Database
	s         EntityStore
	trusted   []string // source id per domain
	untrusted string
	n         int
}

func entityDB(t *testing.T) *entityFixture {
	db := dbtest.New(t)
	ctx := context.Background()

	for _, q := range []string{
		`INSERT INTO domains (base_url) VALUES ('https://d0.example'), ('https://d1.example'), ('https://d2.example')`,
		`INSERT INTO sources (domain_id, adapter, url, enabled) SELECT id, 'rss', base_url || '/feed', true FROM domains`,
		`INSERT INTO sources (domain_id, adapter, url, enabled) SELECT id, 'rss', base_url || '/off', false FROM domains WHERE base_url = 'https://d0.example'`,
		`INSERT INTO tags (key) VALUES ('batteries')`,
		`INSERT INTO tags (key, status) VALUES ('new_tech', 'proposed')`,
		`INSERT INTO tags (key, enabled) VALUES ('old_tech', false)`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	f := &entityFixture{t: t, db: db, s: NewEntityStore(db)}

	for i := range 3 {
		var id string

		if err := db.QueryRow(ctx, `SELECT s.id FROM sources s JOIN domains d ON d.id = s.domain_id WHERE s.enabled AND d.base_url = $1`, fmt.Sprintf("https://d%d.example", i)).Scan(&id); err != nil {
			t.Fatal(err)
		}

		f.trusted = append(f.trusted, id)
	}

	if err := db.QueryRow(ctx, `SELECT id FROM sources WHERE NOT enabled`).Scan(&f.untrusted); err != nil {
		t.Fatal(err)
	}

	return f
}

func (f *entityFixture) exec(qs ...string) {
	f.t.Helper()

	for _, q := range qs {
		if _, err := f.db.Exec(context.Background(), q); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *entityFixture) count(q string, args ...any) (n int) {
	f.t.Helper()

	if err := f.db.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}

	return n
}

// Site picks the trusted source of that domain, or the untrusted one when negative.
func (f *entityFixture) post(site int, lang string) domain.Post {
	f.t.Helper()

	f.n++

	source := f.untrusted
	if site >= 0 {
		source = f.trusted[site]
	}

	p := domain.Post{SourceID: source, Lang: lang}

	if err := f.db.QueryRow(context.Background(), `
		INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content)
		SELECT id, $2, $3, domain_id, 't', 's', 'c' FROM sources WHERE id = $1
		RETURNING id, domain_id
		`, source, fmt.Sprintf("https://post.example/%d", f.n), lang).Scan(&p.ID, &p.DomainID); err != nil {
		f.t.Fatal(err)
	}

	return p
}

func (f *entityFixture) link(p domain.Post, article string, extracted ...ExtractedEntity) []domain.LinkedEntity {
	f.t.Helper()

	ctx := context.Background()
	trusted := p.SourceID != f.untrusted

	var linked []domain.LinkedEntity

	if err := pgx.BeginFunc(ctx, f.db, func(tx pgx.Tx) (err error) {
		linked, err = f.s.LinkEntities(ctx, tx, p, trusted, article, extracted)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}

	return linked
}

func linkedKeys(linked []domain.LinkedEntity) []string {
	keys := make([]string, 0, len(linked))

	for _, e := range linked {
		keys = append(keys, e.Key)
	}

	return keys
}

func (f *entityFixture) seedVolvo() {
	f.t.Helper()

	f.exec(
		`INSERT INTO entities (key, name, kind, status) VALUES ('volvo_cars', 'Volvo Cars', 'company', 'active')`,
		`INSERT INTO entity_aliases (alias, key) VALUES ('volvo_car_corporation', 'volvo_cars')`,
		`INSERT INTO entity_terms (key, lang, term) VALUES ('volvo_cars', 'en', 'Volvo Cars')`,
	)
}

func TestLinkEntitiesCreates(t *testing.T) {
	f := entityDB(t)

	p := f.post(0, "sv")
	linked := f.link(p, "Nya solid state-batterier från Northvolt.", ExtractedEntity{
		Key:  "Solid State Battery",
		Name: "Solid-state battery",
		Term: "solid state-batteri",
		Kind: "technology",
		Tags: []string{"batteries", "nope", "new_tech", "old_tech"},
	})

	want := domain.LinkedEntity{
		Key:    "solid_state_battery",
		Status: domain.StatusProposed,
		Names:  []string{"solid-state battery", "solid state-batteri"},
		Term:   "solid state-batteri",
	}

	if len(linked) != 1 || linked[0].Key != want.Key || linked[0].Status != want.Status || linked[0].Focus || linked[0].Term != want.Term || !slices.Equal(linked[0].Names, want.Names) || len(linked[0].Aliases) != 0 {
		t.Fatalf("linked %+v", linked)
	}

	if n := f.count(`SELECT count(*) FROM entities WHERE key = 'solid_state_battery' AND name = 'Solid-state battery' AND kind = 'technology' AND status = 'proposed' AND enabled`); n != 1 {
		t.Errorf("entity rows %d", n)
	}

	if n := f.count(`SELECT count(*) FROM entity_terms WHERE (key, lang, term) = ('solid_state_battery', 'sv', 'solid state-batteri')`); n != 1 || f.count(`SELECT count(*) FROM entity_terms`) != 1 {
		t.Errorf("terms %d", n)
	}

	if n := f.count(`SELECT count(*) FROM entity_tags WHERE entity = 'solid_state_battery' AND tag = 'batteries'`); n != 1 || f.count(`SELECT count(*) FROM entity_tags`) != 1 {
		t.Errorf("entity tags %d", n)
	}

	if n := f.count(`SELECT count(*) FROM post_entities WHERE post_id = $1 AND entity = 'solid_state_battery'`, p.ID); n != 1 {
		t.Errorf("links %d", n)
	}
}

func TestLinkEntitiesResolves(t *testing.T) {
	f := entityDB(t)
	f.seedVolvo()

	cases := []struct {
		name string
		e    ExtractedEntity
	}{
		{"key", ExtractedEntity{Key: "volvo_cars", Name: "Volvo Cars", Term: "Volvo", Kind: "company"}},
		{"alias", ExtractedEntity{Key: "Volvo Car Corporation", Name: "Volvo Car Corporation", Term: "Volvo Car Corporation", Kind: "company"}},
		{"term", ExtractedEntity{Key: "volvo_personvagnar", Name: "Volvo Personvagnar", Term: "VOLVO CARS", Kind: "company"}},
	}

	for _, c := range cases {
		p := f.post(0, "sv")

		linked := f.link(p, "Volvo Car Corporation, alltså Volvo Cars, ökar försäljningen.", c.e)
		if len(linked) != 1 || linked[0].Key != "volvo_cars" || linked[0].Status != domain.StatusActive || !slices.Equal(linked[0].Aliases, []string{"volvo_car_corporation"}) {
			t.Errorf("%s: linked %+v", c.name, linked)
		}

		if n := f.count(`SELECT count(*) FROM post_entities WHERE post_id = $1 AND entity = 'volvo_cars'`, p.ID); n != 1 {
			t.Errorf("%s: links %d", c.name, n)
		}
	}

	if n := f.count(`SELECT count(*) FROM entities`); n != 1 {
		t.Errorf("entities %d", n)
	}

	if n := f.count(`SELECT count(*) FROM entity_terms WHERE key = 'volvo_cars' AND lang = 'sv' AND lower(term) = 'volvo cars'`); n != 1 {
		t.Errorf("sv term from the term match %d", n)
	}
}

func TestLinkEntitiesUntrusted(t *testing.T) {
	f := entityDB(t)
	f.seedVolvo()

	p := f.post(-1, "sv")
	linked := f.link(p, "Volvo Cars och solid state-batterier.",
		ExtractedEntity{Key: "solid_state_battery", Name: "Solid-state battery", Term: "solid state-batteri", Kind: "technology"},
		ExtractedEntity{Key: "volvo_cars", Name: "Volvo Cars", Term: "Volvo Cars", Kind: "company"},
	)

	if keys := linkedKeys(linked); !slices.Equal(keys, []string{"volvo_cars"}) {
		t.Errorf("linked %v", keys)
	}

	if n := f.count(`SELECT count(*) FROM entities`); n != 1 {
		t.Errorf("entities %d", n)
	}

	if n := f.count(`SELECT count(*) FROM post_entities WHERE post_id = $1`, p.ID); n != 1 {
		t.Errorf("links %d", n)
	}
}

func TestLinkEntitiesDisabled(t *testing.T) {
	f := entityDB(t)
	f.exec(`INSERT INTO entities (key, name, kind, enabled) VALUES ('old_brand', 'Old Brand', 'company', false)`)

	p := f.post(0, "sv")
	if linked := f.link(p, "Old Brand lanserar.", ExtractedEntity{Key: "old_brand", Name: "Old Brand", Term: "Old Brand", Kind: "company"}); len(linked) != 0 {
		t.Errorf("linked %+v", linked)
	}

	if n := f.count(`SELECT count(*) FROM entities WHERE key = 'old_brand' AND NOT enabled`); n != 1 || f.count(`SELECT count(*) FROM entities`) != 1 {
		t.Errorf("entities %d", n)
	}

	if n := f.count(`SELECT count(*) FROM post_entities`) + f.count(`SELECT count(*) FROM entity_terms`); n != 0 {
		t.Errorf("links and terms %d", n)
	}
}

func TestLinkEntitiesGrounding(t *testing.T) {
	f := entityDB(t)

	p := f.post(0, "sv")
	if linked := f.link(p, "Northvolt bygger fabrik.", ExtractedEntity{Key: "solid_state_battery", Name: "Solid-state battery", Term: "solid state-batteri", Kind: "technology"}); len(linked) != 0 {
		t.Errorf("linked %+v", linked)
	}

	if n := f.count(`SELECT count(*) FROM entities`) + f.count(`SELECT count(*) FROM post_entities`); n != 0 {
		t.Errorf("rows %d", n)
	}
}

func TestLinkEntitiesGroundingWhitespace(t *testing.T) {
	f := entityDB(t)

	linked := f.link(f.post(0, "sv"), "Solid  state\u00a0batteri\n\tbyggs ut.", ExtractedEntity{Key: "solid_state_battery", Name: "Solid-state battery", Term: "solid state batteri", Kind: "technology"})
	if !slices.Equal(linkedKeys(linked), []string{"solid_state_battery"}) {
		t.Errorf("linked %+v", linked)
	}
}

func TestLinkEntitiesShortTerm(t *testing.T) {
	f := entityDB(t)
	ai := ExtractedEntity{Key: "artificial_intelligence", Name: "Artificial intelligence", Term: "AI", Kind: "technology"}

	if linked := f.link(f.post(0, "sv"), "A fair price.", ai); len(linked) != 0 {
		t.Errorf("inside a word: %+v", linked)
	}

	if linked := f.link(f.post(0, "sv"), "Bolaget satsar på AI.", ai); !slices.Equal(linkedKeys(linked), []string{"artificial_intelligence"}) {
		t.Errorf("whole word: %+v", linked)
	}
}

func TestLinkEntitiesKind(t *testing.T) {
	f := entityDB(t)

	linked := f.link(f.post(0, "sv"), "Solid state-batterier enligt Anna Svensson.",
		ExtractedEntity{Key: "anna_svensson", Name: "Anna Svensson", Term: "Anna Svensson", Kind: "person"},
		ExtractedEntity{Key: "solid_state_battery", Name: "Solid-state battery", Term: "solid state-batteri", Kind: "Technology"},
	)

	if keys := linkedKeys(linked); !slices.Equal(keys, []string{"solid_state_battery"}) {
		t.Errorf("linked %v", keys)
	}

	if n := f.count(`SELECT count(*) FROM entities WHERE key = 'solid_state_battery' AND kind = 'technology'`); n != 1 || f.count(`SELECT count(*) FROM entities`) != 1 {
		t.Errorf("entities %d", n)
	}
}

func TestLinkEntitiesSameEntityTwice(t *testing.T) {
	f := entityDB(t)
	f.seedVolvo()

	p := f.post(0, "sv")
	linked := f.link(p, "Volvo Cars, alltså Volvo Car Corporation.",
		ExtractedEntity{Key: "volvo_car_corporation", Name: "Volvo Car Corporation", Term: "Volvo Car Corporation", Kind: "company"},
		ExtractedEntity{Key: "volvo_cars", Name: "Volvo Cars", Term: "Volvo Cars", Kind: "company"},
	)

	if len(linked) != 1 || linked[0].Key != "volvo_cars" || linked[0].Term != "Volvo Car Corporation" {
		t.Errorf("linked %+v", linked)
	}

	if n := f.count(`SELECT count(*) FROM post_entities WHERE post_id = $1`, p.ID); n != 1 {
		t.Errorf("links %d", n)
	}
}

func TestLinkEntitiesPromotes(t *testing.T) {
	f := entityDB(t)
	f.exec(
		`INSERT INTO entities (key, name, kind) VALUES ('sodium_ion', 'Sodium-ion battery', 'technology')`,
		`INSERT INTO entity_terms (key, lang, term) VALUES ('sodium_ion', 'en', 'sodium-ion')`,
	)

	e := ExtractedEntity{Key: "sodium_ion", Name: "Sodium-ion battery", Term: "sodium-ion", Kind: "technology"}
	article := "Sodium-ion is coming."

	status := func(linked []domain.LinkedEntity) string {
		t.Helper()

		if len(linked) != 1 || linked[0].Key != "sodium_ion" {
			t.Fatalf("linked %+v", linked)
		}

		return linked[0].Status
	}

	for site := range 2 {
		if s := status(f.link(f.post(site, "en"), article, e)); s != domain.StatusProposed {
			t.Fatalf("post %d: %s", site, s)
		}
	}

	old := f.post(2, "en")
	if _, err := f.db.Exec(context.Background(), `INSERT INTO post_entities (post_id, entity, created_at) VALUES ($1, 'sodium_ion', now() - interval '31 days')`, old.ID); err != nil {
		t.Fatal(err)
	}

	untrusted := f.post(-1, "en")
	if s := status(f.link(untrusted, article, e)); s != domain.StatusProposed {
		t.Fatalf("old and untrusted posts counted: %s", s)
	}

	if s := status(f.link(f.post(2, "en"), article, e)); s != domain.StatusActive {
		t.Fatalf("third trusted post: %s", s)
	}

	if n := f.count(`SELECT count(*) FROM entities WHERE key = 'sodium_ion' AND status = 'active'`); n != 1 {
		t.Errorf("stored status not active")
	}
}

func TestLinkEntitiesPromotionRules(t *testing.T) {
	f := entityDB(t)
	ctx := context.Background()

	f.exec(
		`INSERT INTO entities (key, name, kind) VALUES ('one_domain', 'One domain', 'concept'), ('flagged_source', 'Flagged source', 'concept')`,
		`INSERT INTO sources (domain_id, adapter, url, enabled, status) SELECT id, 'rss', base_url || '/flagged', true, 'flagged' FROM domains WHERE base_url = 'https://d2.example'`,
	)

	support := func(p domain.Post, key string) {
		t.Helper()

		if _, err := f.db.Exec(ctx, `INSERT INTO post_entities (post_id, entity) VALUES ($1, $2)`, p.ID, key); err != nil {
			t.Fatal(err)
		}
	}

	status := func(key string) string {
		t.Helper()

		linked := f.link(f.post(1, "en"), "One domain and flagged source.", ExtractedEntity{Key: key, Name: key, Term: strings.ReplaceAll(key, "_", " "), Kind: "concept"})
		if len(linked) != 1 || linked[0].Key != key {
			t.Fatalf("linked %+v", linked)
		}

		return linked[0].Status
	}

	support(f.post(1, "en"), "one_domain")
	support(f.post(1, "en"), "one_domain")

	if s := status("one_domain"); s != domain.StatusProposed {
		t.Errorf("3 trusted posts on one domain: %s", s)
	}

	flagged := f.post(2, "en")
	if _, err := f.db.Exec(ctx, `UPDATE posts SET source_id = (SELECT id FROM sources WHERE status = 'flagged') WHERE id = $1`, flagged.ID); err != nil {
		t.Fatal(err)
	}

	support(f.post(0, "en"), "flagged_source")
	support(flagged, "flagged_source")

	if s := status("flagged_source"); s != domain.StatusProposed {
		t.Errorf("post from an enabled, flagged source counted: %s", s)
	}
}

func TestPromptKeys(t *testing.T) {
	f := entityDB(t)
	f.exec(
		`INSERT INTO entities (key, name, kind, status, focus) VALUES ('z_focus', 'Z', 'concept', 'proposed', true)`,
		`INSERT INTO entities (key, name, kind, status, focus, enabled) VALUES ('a_disabled', 'A', 'concept', 'active', true, false)`,
		`INSERT INTO entities (key, name, kind, status) VALUES ('active_weak', 'A', 'concept', 'active'), ('active_strong', 'A', 'concept', 'active')`,
		`INSERT INTO entities (key, name, kind) VALUES ('proposed_strong', 'P', 'concept'), ('proposed_weak', 'P', 'concept')`,
	)

	for i, l := range []struct {
		key string
		age int
	}{
		{"active_strong", 0}, {"active_strong", 0},
		{"active_weak", 31}, {"active_weak", 31}, {"active_weak", 31},
		{"proposed_strong", 0}, {"proposed_strong", 0}, {"proposed_strong", 0},
	} {
		p := f.post(i%3, "sv")
		if _, err := f.db.Exec(context.Background(), `INSERT INTO post_entities (post_id, entity, created_at) VALUES ($1, $2, now() - make_interval(days => $3))`, p.ID, l.key, l.age); err != nil {
			t.Fatal(err)
		}
	}

	keys, err := f.s.PromptKeys(context.Background())
	if want := []string{"z_focus", "active_strong", "active_weak", "proposed_strong", "proposed_weak"}; err != nil || !slices.Equal(keys, want) {
		t.Fatalf("got %v, %v", keys, err)
	}

	f.exec(`INSERT INTO entities (key, name, kind) SELECT 'filler_' || i, 'F', 'concept' FROM generate_series(1, 300) i`)

	keys, err = f.s.PromptKeys(context.Background())
	if err != nil || len(keys) != 300 || !slices.Equal(keys[:5], []string{"z_focus", "active_strong", "active_weak", "proposed_strong", "filler_1"}) {
		t.Errorf("capped: %d %v, %v", len(keys), keys[:min(len(keys), 5)], err)
	}
}

func (f *entityFixture) linkKeys(p domain.Post, keys ...string) {
	f.t.Helper()

	for _, k := range keys {
		f.exec(fmt.Sprintf(`INSERT INTO post_entities (post_id, entity) VALUES ('%s', '%s')`, p.ID, k))
	}
}

func publicKeys(es []domain.PublicEntity) []string {
	keys := make([]string, 0, len(es))

	for _, e := range es {
		keys = append(keys, e.Key)
	}

	return keys
}

func TestListPublicEntities(t *testing.T) {
	f := entityDB(t)
	ctx := context.Background()

	f.exec(
		`INSERT INTO entities (key, name, kind, status, focus) VALUES
			('a_weak', 'A', 'company', 'active', true), ('b_strong', 'B', 'technology', 'active', false),
			('c_tie', 'C', 'company', 'active', false), ('d_tie', 'D', 'company', 'active', false)`,
		`INSERT INTO entities (key, name, kind) VALUES ('e_proposed', 'E', 'company')`,
		`INSERT INTO entities (key, name, kind, status, enabled) VALUES ('f_disabled', 'F', 'company', 'active', false)`,
		`INSERT INTO entity_terms (key, lang, term) VALUES ('a_weak', 'sv', 'ett'), ('a_weak', 'sv', 'en'), ('a_weak', 'en', 'one')`,
		`INSERT INTO tags (key) VALUES ('electric')`,
		`INSERT INTO categories (key) VALUES ('energy'), ('hidden'), ('proposed_only')`,
		`INSERT INTO categories (key, enabled) VALUES ('off', false)`,
		`INSERT INTO category_tags (category, tag) VALUES
			('energy', 'batteries'), ('energy', 'electric'), ('off', 'batteries'), ('hidden', 'old_tech'), ('proposed_only', 'new_tech')`,
		`INSERT INTO entity_tags (entity, tag) VALUES
			('a_weak', 'electric'), ('b_strong', 'batteries'), ('b_strong', 'electric'), ('b_strong', 'new_tech'),
			('c_tie', 'old_tech'), ('d_tie', 'new_tech'), ('e_proposed', 'batteries'), ('f_disabled', 'batteries')`,
	)

	for i, key := range []string{"b_strong", "b_strong", "a_weak", "c_tie", "d_tie"} {
		f.linkKeys(f.post(i%3, "sv"), key)
	}

	// Outside the strength window.
	f.exec(`UPDATE post_entities SET created_at = now() - interval '31 days' WHERE entity = 'd_tie'`)

	list := func(filter domain.EntityFilter) []string {
		es, err := f.s.ListPublic(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}

		return publicKeys(es)
	}

	all := []string{"b_strong", "a_weak", "c_tie", "d_tie"}

	cases := map[string]struct {
		filter domain.EntityFilter
		want   []string
	}{
		"strength then key":    {domain.EntityFilter{}, all},
		"kind":                 {domain.EntityFilter{Kind: "technology"}, []string{"b_strong"}},
		"unknown kind":         {domain.EntityFilter{Kind: "person"}, nil},
		"tag":                  {domain.EntityFilter{Tag: "electric"}, []string{"b_strong", "a_weak"}},
		"proposed tag":         {domain.EntityFilter{Tag: "new_tech"}, nil},
		"disabled tag":         {domain.EntityFilter{Tag: "old_tech"}, nil},
		"category":             {domain.EntityFilter{Category: "energy"}, []string{"b_strong", "a_weak"}},
		"disabled category":    {domain.EntityFilter{Category: "off"}, nil},
		"category of hidden":   {domain.EntityFilter{Category: "hidden"}, nil},
		"category of proposed": {domain.EntityFilter{Category: "proposed_only"}, nil},
		"unknown category":     {domain.EntityFilter{Category: "nope"}, nil},
		"kind and tag":         {domain.EntityFilter{Kind: "company", Tag: "electric"}, []string{"a_weak"}},
		"limit":                {domain.EntityFilter{Filter: query.Filter{Limit: "2"}}, all[:2]},
		"offset":               {domain.EntityFilter{Filter: query.Filter{Limit: "2", Offset: "2"}}, all[2:]},
	}

	for name, c := range cases {
		if got := list(c.filter); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}

	es, err := f.s.ListPublic(ctx, domain.EntityFilter{})
	if err != nil {
		t.Fatal(err)
	}

	b, a := es[0], es[1]
	if b.Strength != 2 || a.Strength != 1 || es[3].Strength != 0 {
		t.Errorf("strength %d %d %d", b.Strength, a.Strength, es[3].Strength)
	}

	if !slices.Equal(b.Tags, []string{"batteries", "electric"}) || b.Name != "B" || b.Kind != "technology" {
		t.Errorf("b_strong %+v", b)
	}

	if !slices.Equal(a.Terms["sv"], []string{"en", "ett"}) || !slices.Equal(a.Terms["en"], []string{"one"}) {
		t.Errorf("a_weak terms %v", a.Terms)
	}

	if es[2].Tags == nil || es[2].Terms == nil || len(es[2].Tags) != 0 || len(es[2].Terms) != 0 {
		t.Errorf("c_tie tags %v, terms %v", es[2].Tags, es[2].Terms)
	}
}

func TestGetPublicEntity(t *testing.T) {
	f := entityDB(t)
	ctx := context.Background()

	f.exec(
		`INSERT INTO entities (key, name, kind, status) VALUES ('volvo_cars', 'Volvo Cars', 'company', 'active')`,
		`INSERT INTO entities (key, name, kind) VALUES ('northvolt', 'Northvolt', 'company')`,
		`INSERT INTO entities (key, name, kind, status, enabled) VALUES ('off', 'Off', 'company', 'active', false)`,
		`INSERT INTO entity_aliases (alias, key) VALUES ('volvo_car_corporation', 'volvo_cars'), ('northvolt_ab', 'northvolt'), ('off_alias', 'off')`,
		`INSERT INTO entity_terms (key, lang, term) VALUES ('volvo_cars', 'sv', 'Volvo Cars')`,
		`INSERT INTO entity_tags (entity, tag) VALUES ('volvo_cars', 'batteries'), ('volvo_cars', 'new_tech'), ('volvo_cars', 'old_tech')`,
	)

	f.linkKeys(f.post(0, "sv"), "volvo_cars")

	for _, key := range []string{"volvo_cars", "volvo_car_corporation"} {
		e, err := f.s.GetPublic(ctx, key)
		if err != nil || e.Key != "volvo_cars" || e.Name != "Volvo Cars" || e.Strength != 1 || !slices.Equal(e.Tags, []string{"batteries"}) || !slices.Equal(e.Terms["sv"], []string{"Volvo Cars"}) {
			t.Errorf("%s: %+v, %v", key, e, err)
		}
	}

	for _, key := range []string{"northvolt", "northvolt_ab", "off", "off_alias", "nope"} {
		if _, err := f.s.GetPublic(ctx, key); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s: err %v", key, err)
		}
	}
}
