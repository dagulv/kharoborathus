package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/lib/query"
	"github.com/jackc/pgx/v5"
)

func TestSavePost(t *testing.T) {
	db, _, base := rawPostDB(t)
	ctx := context.Background()
	s := NewPostStore(db)
	p := domain.Post{SourceID: base.SourceID, DomainID: base.DomainID, URL: "https://a.example/1", Lang: "sv", Title: "t", ShortContent: "s", Content: "c", Tags: []string{"acquisition"}}
	contentAt := time.Now().Add(-time.Hour)

	save := func(p domain.Post, contentAt time.Time) (out domain.Post, saved bool) {
		t.Helper()

		if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) (err error) {
			out, saved, err = s.SavePost(ctx, tx, p, contentAt)
			return err
		}); err != nil {
			t.Fatal(err)
		}

		return out, saved
	}

	first, saved := save(p, contentAt)
	if !saved {
		t.Fatal("first save not saved")
	}

	if _, saved := save(p, contentAt); saved {
		t.Error("second save of the same content saved")
	}

	if current, err := s.PostCurrent(ctx, p.URL, contentAt); err != nil || !current {
		t.Errorf("current %v, %v", current, err)
	}

	resynced := time.Now().Add(time.Minute)

	if current, err := s.PostCurrent(ctx, p.URL, resynced); err != nil || current {
		t.Errorf("current after resync %v, %v", current, err)
	}

	p.Title, p.Tags = "t2", []string{"buyback"}

	second, saved := save(p, resynced)
	if !saved || second.ID != first.ID || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("resave saved %v, id %s want %s, created %v want %v", saved, second.ID, first.ID, second.CreatedAt, first.CreatedAt)
	}

	var title string
	var tags []string

	if err := db.QueryRow(ctx, `SELECT title, tags FROM posts WHERE id = $1`, first.ID).Scan(&title, &tags); err != nil || title != "t2" || !slices.Equal(tags, p.Tags) {
		t.Errorf("title %q, tags %v, %v", title, tags, err)
	}
}

func TestTrusted(t *testing.T) {
	db, _, base := rawPostDB(t)
	ctx := context.Background()
	s := NewPostStore(db)

	cases := []struct {
		enabled bool
		status  string
		want    bool
	}{
		{true, domain.StatusActive, true},
		{false, domain.StatusActive, false},
		{true, domain.StatusProposed, false},
	}

	for _, c := range cases {
		if _, err := db.Exec(ctx, `UPDATE sources SET enabled = $2, status = $3 WHERE id = $1`, base.SourceID, c.enabled, c.status); err != nil {
			t.Fatal(err)
		}

		if got, err := s.Trusted(ctx, base.SourceID); err != nil || got != c.want {
			t.Errorf("%v %s: got %v, %v", c.enabled, c.status, got, err)
		}
	}
}

func postsDB(t *testing.T) (*database.Database, PostStore, domain.Post, domain.Post) {
	db, _, base := rawPostDB(t)
	ctx := context.Background()

	for _, q := range []string{
		`INSERT INTO tags (key) VALUES ('acquisition')`,
		`INSERT INTO tags (key, status) VALUES ('new_factory', 'proposed')`,
		`INSERT INTO tag_terms (key, lang, term) VALUES ('acquisition', 'sv', 'förvärv')`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	s := NewPostStore(db)

	insert := func(url, title string, tags []string, age string) domain.Post {
		p := domain.Post{SourceID: base.SourceID, DomainID: base.DomainID, URL: url, Lang: "sv", Title: title, ShortContent: "s", Content: "c", Tags: tags}

		if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) (err error) {
			p, _, err = s.SavePost(ctx, tx, p, time.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}

		if _, err := db.Exec(ctx, `UPDATE posts SET created_at = now() - $2::interval WHERE id = $1`, p.ID, age); err != nil {
			t.Fatal(err)
		}

		return p
	}

	volvo := insert("https://a.example/volvo", "Volvo köper Northvolt", []string{"acquisition", "new_factory"}, "2 hours")
	ericsson := insert("https://a.example/ericsson", "Ericsson rapporterar", nil, "1 hour")

	return db, s, volvo, ericsson
}

func listIDs(t *testing.T, s PostStore, f domain.PostFilter) (ids []string, posts []domain.Post) {
	t.Helper()

	for p, err := range s.ListPosts(context.Background(), f) {
		if err != nil {
			t.Fatal(err)
		}

		ids = append(ids, p.ID)
		posts = append(posts, p)
	}

	return ids, posts
}

func TestListPosts(t *testing.T) {
	_, s, volvo, ericsson := postsDB(t)

	ids, posts := listIDs(t, s, domain.PostFilter{})
	if !slices.Equal(ids, []string{ericsson.ID, volvo.ID}) {
		t.Fatalf("ids %v", ids)
	}

	if !slices.Equal(posts[1].Tags, []string{"acquisition"}) || posts[1].Title != volvo.Title || posts[1].Content != "" {
		t.Errorf("volvo %+v", posts[1])
	}

	cases := map[string]struct {
		filter domain.PostFilter
		want   []string
	}{
		"active tag":   {domain.PostFilter{Tags: []string{"acquisition"}}, []string{volvo.ID}},
		"proposed tag": {domain.PostFilter{Tags: []string{"new_factory"}}, nil},
		"title search": {domain.PostFilter{Filter: query.Filter{Search: "Northvolt"}}, []string{volvo.ID}},
		"term search":  {domain.PostFilter{Filter: query.Filter{Search: "förvärv"}}, []string{volvo.ID}},
		"limit":        {domain.PostFilter{Filter: query.Filter{Limit: "1"}}, []string{ericsson.ID}},
		"offset":       {domain.PostFilter{Filter: query.Filter{Limit: "1", Offset: "1"}}, []string{volvo.ID}},
		"oldest first": {domain.PostFilter{Filter: query.Filter{OrderBy: "createdAt", Order: "asc"}}, []string{volvo.ID, ericsson.ID}},
	}

	for name, c := range cases {
		if ids, _ := listIDs(t, s, c.filter); !slices.Equal(ids, c.want) {
			t.Errorf("%s: got %v", name, ids)
		}
	}
}

func addPost(t *testing.T, db *database.Database, like domain.Post, url string, tags []string, age string) string {
	t.Helper()

	var id string
	if err := db.QueryRow(context.Background(), `
		INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content, tags, created_at)
		VALUES ($1, $2, 'sv', $3, $2, 's', 'c', $4, now() - $5::interval)
		RETURNING id
		`, like.SourceID, url, like.DomainID, tags, age).Scan(&id); err != nil {
		t.Fatal(err)
	}

	return id
}

func execAll(t *testing.T, db *database.Database, qs ...string) {
	t.Helper()

	for _, q := range qs {
		if _, err := db.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListPostsCategory(t *testing.T) {
	db, s, volvo, ericsson := postsDB(t)

	execAll(t, db,
		`INSERT INTO tags (key) VALUES ('dividend'), ('layoffs')`,
		`INSERT INTO categories (key) VALUES ('finance'), ('macro'), ('factory')`,
		`INSERT INTO categories (key, enabled) VALUES ('off', false)`,
		`INSERT INTO category_tags (category, tag) VALUES
			('finance', 'acquisition'), ('finance', 'dividend'), ('macro', 'dividend'), ('macro', 'layoffs'),
			('factory', 'new_factory'), ('off', 'acquisition')`,
	)

	payout := addPost(t, db, volvo, "https://a.example/payout", []string{"dividend"}, "30 minutes")
	cuts := addPost(t, db, volvo, "https://a.example/cuts", []string{"layoffs"}, "10 minutes")

	cases := map[string]struct {
		categories []string
		want       []string
	}{
		"one category":      {[]string{"finance"}, []string{payout, volvo.ID}},
		"other category":    {[]string{"macro"}, []string{cuts, payout}},
		"both categories":   {[]string{"finance", "macro"}, []string{payout}},
		"disabled category": {[]string{"off"}, nil},
		"unknown category":  {[]string{"nope"}, nil},
		"proposed tag only": {[]string{"factory"}, nil},
		"no category":       {nil, []string{cuts, payout, ericsson.ID, volvo.ID}},
	}

	for name, c := range cases {
		if ids, _ := listIDs(t, s, domain.PostFilter{Categories: c.categories}); !slices.Equal(ids, c.want) {
			t.Errorf("%s: got %v, want %v", name, ids, c.want)
		}
	}
}

func TestListPostsCategoryWithTagAndAfter(t *testing.T) {
	db, s, volvo, ericsson := postsDB(t)

	execAll(t, db,
		`INSERT INTO tags (key) VALUES ('dividend')`,
		`INSERT INTO tags (key, enabled) VALUES ('layoffs', false)`,
		`INSERT INTO categories (key) VALUES ('income'), ('dead')`,
		`INSERT INTO category_tags (category, tag) VALUES ('income', 'dividend'), ('dead', 'layoffs')`,
	)

	addPost(t, db, volvo, "https://a.example/newest", []string{"acquisition", "dividend"}, "30 minutes")
	addPost(t, db, volvo, "https://a.example/dividend-only", []string{"dividend"}, "10 minutes")
	oldest := addPost(t, db, volvo, "https://a.example/oldest", []string{"acquisition", "dividend"}, "3 hours")
	addPost(t, db, volvo, "https://a.example/layoffs", []string{"layoffs"}, "20 minutes")

	f := domain.PostFilter{Categories: []string{"income"}, Tags: []string{"acquisition"}, After: ericsson.ID}
	if ids, _ := listIDs(t, s, f); !slices.Equal(ids, []string{oldest}) {
		t.Errorf("combined: got %v, want %v", ids, []string{oldest})
	}

	if ids, _ := listIDs(t, s, domain.PostFilter{Categories: []string{"dead"}}); len(ids) != 0 {
		t.Errorf("category with only a disabled tag: got %v", ids)
	}
}

func TestListPostsEntity(t *testing.T) {
	db, s, volvo, ericsson := postsDB(t)

	execAll(t, db,
		`INSERT INTO entities (key, name, kind, status) VALUES ('volvo_cars', 'Volvo Cars', 'company', 'active'), ('northvolt', 'Northvolt', 'company', 'active')`,
		`INSERT INTO entities (key, name, kind) VALUES ('new_factory', 'New factory', 'concept')`,
		`INSERT INTO entities (key, name, kind, status, enabled) VALUES ('off', 'Off', 'company', 'active', false)`,
		fmt.Sprintf(`INSERT INTO post_entities (post_id, entity) VALUES
			('%[1]s', 'volvo_cars'), ('%[1]s', 'northvolt'), ('%[1]s', 'new_factory'), ('%[1]s', 'off'), ('%[2]s', 'volvo_cars')`, volvo.ID, ericsson.ID),
	)

	cases := map[string]struct {
		entities []string
		want     []string
	}{
		"one entity":      {[]string{"volvo_cars"}, []string{ericsson.ID, volvo.ID}},
		"other entity":    {[]string{"northvolt"}, []string{volvo.ID}},
		"both entities":   {[]string{"volvo_cars", "northvolt"}, []string{volvo.ID}},
		"proposed entity": {[]string{"new_factory"}, nil},
		"disabled entity": {[]string{"off"}, nil},
		"unknown entity":  {[]string{"nope"}, nil},
		"one unknown":     {[]string{"volvo_cars", "nope"}, nil},
		"no entity":       {nil, []string{ericsson.ID, volvo.ID}},
	}

	for name, c := range cases {
		if ids, _ := listIDs(t, s, domain.PostFilter{Entities: c.entities}); !slices.Equal(ids, c.want) {
			t.Errorf("%s: got %v, want %v", name, ids, c.want)
		}
	}

	_, posts := listIDs(t, s, domain.PostFilter{Filter: query.Filter{OrderBy: "createdAt", Order: "asc"}})
	if !slices.Equal(posts[0].Entities, []string{"northvolt", "volvo_cars"}) {
		t.Errorf("volvo entities %v", posts[0].Entities)
	}

	if posts[1].Entities == nil || len(posts[1].Entities) != 1 {
		t.Errorf("ericsson entities %v", posts[1].Entities)
	}

	execAll(t, db, `DELETE FROM post_entities WHERE post_id = '`+ericsson.ID+`'`)

	if got, err := s.GetPost(context.Background(), ericsson.ID); err != nil || got.Entities == nil || len(got.Entities) != 0 {
		t.Errorf("no entities: %v, %v", got.Entities, err)
	}

	if got, err := s.GetPost(context.Background(), volvo.ID); err != nil || !slices.Equal(got.Entities, []string{"northvolt", "volvo_cars"}) {
		t.Errorf("get: %v, %v", got.Entities, err)
	}
}

func TestGetPost(t *testing.T) {
	_, s, volvo, _ := postsDB(t)
	ctx := context.Background()

	got, err := s.GetPost(ctx, volvo.ID)
	if err != nil || got.ID != volvo.ID || got.Content != "c" || !slices.Equal(got.Tags, []string{"acquisition"}) {
		t.Errorf("got %+v, %v", got, err)
	}

	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
		if _, err := s.GetPost(ctx, id); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s: err %v", id, err)
		}
	}
}

func TestListPostsFeedOrder(t *testing.T) {
	db, s, volvo, ericsson := postsDB(t)
	ctx := context.Background()

	// Published long ago but stored last, like a backfilled article.
	if _, err := db.Exec(ctx, `UPDATE posts SET posted_at = now() - interval '30 days' WHERE id = $1`, ericsson.ID); err != nil {
		t.Fatal(err)
	}

	ids, posts := listIDs(t, s, domain.PostFilter{})
	if !slices.Equal(ids, []string{volvo.ID, ericsson.ID}) {
		t.Errorf("ids %v", ids)
	}

	if posts[0].DomainURL != "https://a.example" {
		t.Errorf("domain url %q", posts[0].DomainURL)
	}
}

func TestListPostsAfter(t *testing.T) {
	_, s, volvo, ericsson := postsDB(t)

	if ids, _ := listIDs(t, s, domain.PostFilter{After: ericsson.ID}); !slices.Equal(ids, []string{volvo.ID}) {
		t.Errorf("after ericsson: %v", ids)
	}

	if ids, _ := listIDs(t, s, domain.PostFilter{After: volvo.ID}); len(ids) != 0 {
		t.Errorf("after the last: %v", ids)
	}
}

func TestListPostsAfterInvalid(t *testing.T) {
	_, s, volvo, _ := postsDB(t)

	cases := map[string]domain.PostFilter{
		"unknown id": {After: "00000000-0000-0000-0000-000000000000"},
		"not a uuid": {After: "nope"},
		"with order": {After: volvo.ID, Filter: query.Filter{OrderBy: "title"}},
	}

	for name, f := range cases {
		for _, err := range s.ListPosts(context.Background(), f) {
			if !errors.Is(err, domain.ErrInvalidFilter) {
				t.Errorf("%s: err %v", name, err)
			}
		}
	}
}

func TestListPostsTagFilterUsesIndex(t *testing.T) {
	db, _, _, _ := postsDB(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}

	var rows []string
	r, err := db.Query(ctx, `EXPLAIN `+listPostsQuery+` WHERE `+tagFilter(1)+` ORDER BY `+feedOrder+` LIMIT 30`, []string{"acquisition"})
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var line string
		if err := r.Scan(&line); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, line)
	}

	if !slices.ContainsFunc(rows, func(l string) bool { return strings.Contains(l, "posts_tags_idx") }) {
		t.Errorf("plan does not use posts_tags_idx:\n%s", strings.Join(rows, "\n"))
	}
}

func TestListPostsCategoryFilterUsesIndex(t *testing.T) {
	db, _, _, _ := postsDB(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}

	var rows []string
	r, err := db.Query(ctx, `EXPLAIN `+listPostsQuery+` WHERE `+categoryFilter(1)+` ORDER BY `+feedOrder+` LIMIT 30`, "deals")
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var line string
		if err := r.Scan(&line); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, line)
	}

	if !slices.ContainsFunc(rows, func(l string) bool { return strings.Contains(l, "posts_tags_idx") }) {
		t.Errorf("plan does not use posts_tags_idx:\n%s", strings.Join(rows, "\n"))
	}
}

func TestFeedOrderUsesIndex(t *testing.T) {
	db, _, _, _ := postsDB(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}

	r, err := db.Query(ctx, `EXPLAIN `+listPostsQuery+` WHERE true ORDER BY `+feedOrder+` LIMIT 30`)
	if err != nil {
		t.Fatal(err)
	}

	var rows []string
	for r.Next() {
		var line string
		if err := r.Scan(&line); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, line)
	}

	if !slices.ContainsFunc(rows, func(l string) bool { return strings.Contains(l, "posts_feed_idx") }) {
		t.Errorf("plan does not use posts_feed_idx:\n%s", strings.Join(rows, "\n"))
	}
}

func TestListAdminPosts(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	domainA, sourceA := seedSource(t, db, "https://a.example", "https://a.example/feed")
	domainB, sourceB := seedSource(t, db, "https://b.example", "https://b.example/feed")

	newest := seedPost(t, db, sourceA, domainA, "https://a.example/1", "en", "Volvo buys back shares", []string{"buyback", "proposed-x"}, "1 hour", "1 minute")
	seedPost(t, db, sourceA, domainA, "https://a.example/2", "sv", "Kvartalsrapport", []string{"report"}, "2 hours", "2 minutes")
	oldest := seedPost(t, db, sourceB, domainB, "https://b.example/1", "en", "Weather today", nil, "3 hours", "3 minutes")

	if _, err := db.Exec(ctx, `
		INSERT INTO tags (key, status) VALUES ('buyback', 'active'), ('proposed-x', 'proposed');
		INSERT INTO categories (key, enabled) VALUES ('finance', false), ('empty', true);
		INSERT INTO category_tags (category, tag) VALUES ('finance', 'proposed-x');
		INSERT INTO entities (key, name, kind) VALUES ('volvo_cars', 'Volvo Cars', 'company'), ('unlinked', 'Unlinked', 'company');
		`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(ctx, `INSERT INTO post_entities (post_id, entity) VALUES ($1, 'volvo_cars')`, newest); err != nil {
		t.Fatal(err)
	}

	s := NewPostStore(db)

	all, err := s.ListAdminPosts(ctx, domain.AdminPostFilter{Page: 1})
	if err != nil || all.Total != 3 || len(all.Posts) != 3 || all.Page != 1 || all.PageSize != 50 {
		t.Fatalf("all: %+v, %v", all, err)
	}

	first, last := all.Posts[0], all.Posts[2]

	if first.PostID == nil || *first.PostID != newest || last.PostID == nil || *last.PostID != oldest || first.Content != "" || first.DomainURL != "https://a.example" {
		t.Errorf("order or fields: %+v", all.Posts)
	}

	if first.Status != "published" || first.Title != "Volvo buys back shares" || len(first.Tags) != 2 || first.ShortContent != "summary" {
		t.Errorf("published fields: %+v", first)
	}

	cases := map[string]struct {
		filter domain.AdminPostFilter
		want   int
	}{
		"source":           {domain.AdminPostFilter{SourceID: sourceB, Page: 1}, 1},
		"active tag":       {domain.AdminPostFilter{Tag: "buyback", Page: 1}, 1},
		"proposed tag":     {domain.AdminPostFilter{Tag: "proposed-x", Page: 1}, 1},
		"search":           {domain.AdminPostFilter{Search: "shares", Page: 1}, 1},
		"source+search":    {domain.AdminPostFilter{SourceID: sourceB, Search: "shares", Page: 1}, 0},
		"category":         {domain.AdminPostFilter{Category: "finance", Page: 1}, 1},
		"empty category":   {domain.AdminPostFilter{Category: "empty", Page: 1}, 0},
		"unknown category": {domain.AdminPostFilter{Category: "nope", Page: 1}, 0},
		"entity":           {domain.AdminPostFilter{Entity: "volvo_cars", Page: 1}, 1},
		"unlinked entity":  {domain.AdminPostFilter{Entity: "unlinked", Page: 1}, 0},
		"unknown entity":   {domain.AdminPostFilter{Entity: "nope", Page: 1}, 0},
		"published":        {domain.AdminPostFilter{Status: "published", Page: 1}, 3},
		"rejected":         {domain.AdminPostFilter{Status: "rejected", Page: 1}, 0},
	}

	for name, c := range cases {
		got, err := s.ListAdminPosts(ctx, c.filter)
		if err != nil || got.Total != c.want || len(got.Posts) != c.want {
			t.Errorf("%s: %d/%d, %v", name, len(got.Posts), got.Total, err)
		}
	}

	page2, err := s.ListAdminPosts(ctx, domain.AdminPostFilter{Page: 2})
	if err != nil || page2.Posts == nil || page2.Total != 3 || len(page2.Posts) != 0 {
		t.Errorf("page 2: %+v, %v", page2, err)
	}
}

func TestListAdminPostsRaw(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	domainID, sourceID := seedSource(t, db, "https://a.example", "https://a.example/feed")

	seedPost(t, db, sourceID, domainID, "https://a.example/published", "en", "Published post", []string{"buyback"}, "1 hour", "4 minutes")
	rejected := seedRawPost(t, db, sourceID, domainID, "https://a.example/rejected", "rejected", `{"title": "Design with Claude", "content": "body", "authors": ["A"]}`, "3 minutes")
	missing := seedRawPost(t, db, sourceID, domainID, "https://a.example/missing", "missing", "", "2 minutes")
	failed := seedRawPost(t, db, "", domainID, "https://a.example/failed", "failed", "", "1 minute")

	s := NewPostStore(db)

	all, err := s.ListAdminPosts(ctx, domain.AdminPostFilter{Page: 1})
	if err != nil || all.Total != 4 || len(all.Posts) != 4 {
		t.Fatalf("all: %+v, %v", all, err)
	}

	ids := []string{all.Posts[0].ID, all.Posts[1].ID, all.Posts[2].ID}
	if !slices.Equal(ids, []string{failed, missing, rejected}) || all.Posts[3].Status != "published" {
		t.Errorf("order %v, last %+v", ids, all.Posts[3])
	}

	if f := all.Posts[0]; f.Status != "failed" || f.SourceID != nil || f.Title != "" || f.URL != "https://a.example/failed" || f.PostID != nil {
		t.Errorf("failed row %+v", f)
	}

	if r := all.Posts[2]; r.Title != "Design with Claude" || !slices.Equal(r.Authors, []string{"A"}) || len(r.Tags) != 0 {
		t.Errorf("rejected row %+v", r)
	}

	cases := map[string]struct {
		filter domain.AdminPostFilter
		want   int
	}{
		"rejected":     {domain.AdminPostFilter{Status: "rejected", Page: 1}, 1},
		"missing":      {domain.AdminPostFilter{Status: "missing", Page: 1}, 1},
		"raw title":    {domain.AdminPostFilter{Search: "Claude", Page: 1}, 1},
		"url":          {domain.AdminPostFilter{Search: "a.example/fail", Page: 1}, 1},
		"tag":          {domain.AdminPostFilter{Tag: "buyback", Page: 1}, 1},
		"source":       {domain.AdminPostFilter{SourceID: sourceID, Page: 1}, 3},
		"tag+rejected": {domain.AdminPostFilter{Tag: "buyback", Status: "rejected", Page: 1}, 0},
	}

	for name, c := range cases {
		got, err := s.ListAdminPosts(ctx, c.filter)
		if err != nil || got.Total != c.want || len(got.Posts) != c.want {
			t.Errorf("%s: %d/%d, %v", name, len(got.Posts), got.Total, err)
		}
	}
}

func TestListAdminPostsOddSearch(t *testing.T) {
	db := dbtest.New(t)
	s := NewPostStore(db)

	for _, q := range []string{`"`, "-", "!!!", `"unclosed phrase`, "a & | ! b"} {
		if _, err := s.ListAdminPosts(context.Background(), domain.AdminPostFilter{Search: q, Page: 1}); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
}

func TestGetAdminPost(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	domainID, sourceID := seedSource(t, db, "https://a.example", "https://a.example/feed")
	postID := seedPost(t, db, sourceID, domainID, "https://a.example/1", "en", "One", []string{"proposed-x"}, "0", "0")
	rejected := seedRawPost(t, db, sourceID, domainID, "https://a.example/rejected", "rejected", `{"title": "Two", "content": "raw body"}`, "0")
	orphan := seedRawPost(t, db, "", domainID, "https://a.example/orphan", "failed", "", "0")

	s := NewPostStore(db)

	list, err := s.ListAdminPosts(ctx, domain.AdminPostFilter{Status: "published", Page: 1})
	if err != nil || len(list.Posts) != 1 {
		t.Fatalf("list %+v, %v", list, err)
	}

	p, err := s.GetAdminPost(ctx, list.Posts[0].ID)
	if err != nil || p.SourceURL != "https://a.example/feed" || p.Content != "body" || len(p.Tags) != 1 || *p.PostID != postID || p.Status != "published" {
		t.Errorf("published %+v, %v", p, err)
	}

	if _, err := db.Exec(ctx, `UPDATE raw_posts SET status = 'pending' WHERE id = $1`, list.Posts[0].ID); err != nil {
		t.Fatal(err)
	}

	// A published post being resynced shows as in the pipeline.
	if p, err := s.GetAdminPost(ctx, list.Posts[0].ID); err != nil || p.Status != "pending" || p.PostID == nil {
		t.Errorf("resyncing %+v, %v", p, err)
	}

	if p, err := s.GetAdminPost(ctx, rejected); err != nil || p.Content != "raw body" || p.Title != "Two" || p.Status != "rejected" {
		t.Errorf("rejected %+v, %v", p, err)
	}

	if p, err := s.GetAdminPost(ctx, orphan); err != nil || p.SourceURL != "" || p.SourceID != nil {
		t.Errorf("orphan %+v, %v", p, err)
	}

	for _, bad := range []string{"00000000-0000-0000-0000-000000000000", "x"} {
		if _, err := s.GetAdminPost(ctx, bad); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}
