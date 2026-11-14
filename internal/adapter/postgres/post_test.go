package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/lib/query"
	"github.com/jackc/pgx/v5"
)

func TestInsertPost(t *testing.T) {
	db, _, base := rawPostDB(t)
	ctx := context.Background()
	s := NewPostStore(db)
	p := domain.Post{SourceID: base.SourceID, DomainID: base.DomainID, URL: "https://a.example/1", Lang: "sv", Title: "t", ShortContent: "s", Content: "c", Tags: []string{"acquisition"}}

	for i, want := range []bool{true, false} {
		var inserted bool

		if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) (err error) {
			_, inserted, err = s.InsertPost(ctx, tx, p)
			return err
		}); err != nil || inserted != want {
			t.Errorf("insert %d: inserted %v, %v", i, inserted, err)
		}
	}

	if exists, err := s.PostExists(ctx, p.URL); err != nil || !exists {
		t.Errorf("exists %v, %v", exists, err)
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
			p, _, err = s.InsertPost(ctx, tx, p)
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

	newest := seedPost(t, db, sourceA, domainA, "https://a.example/1", "en", "Volvo buys back shares", []string{"buyback", "proposed-x"}, "1 hour", "0")
	seedPost(t, db, sourceA, domainA, "https://a.example/2", "sv", "Kvartalsrapport", []string{"report"}, "2 hours", "0")
	oldest := seedPost(t, db, sourceB, domainB, "https://b.example/1", "en", "Weather today", nil, "3 hours", "0")

	s := NewPostStore(db)

	all, err := s.ListAdminPosts(ctx, domain.AdminPostFilter{Page: 1})
	if err != nil || all.Total != 3 || len(all.Posts) != 3 || all.Page != 1 || all.PageSize != 50 {
		t.Fatalf("all: %+v, %v", all, err)
	}

	if all.Posts[0].ID != newest || all.Posts[2].ID != oldest || all.Posts[0].Content != "" || all.Posts[0].DomainURL != "https://a.example" {
		t.Errorf("order or fields: %+v", all.Posts)
	}

	cases := map[string]struct {
		filter domain.AdminPostFilter
		want   int
	}{
		"source":        {domain.AdminPostFilter{SourceID: sourceB, Page: 1}, 1},
		"active tag":    {domain.AdminPostFilter{Tag: "buyback", Page: 1}, 1},
		"proposed tag":  {domain.AdminPostFilter{Tag: "proposed-x", Page: 1}, 1},
		"search":        {domain.AdminPostFilter{Search: "shares", Page: 1}, 1},
		"source+search": {domain.AdminPostFilter{SourceID: sourceB, Search: "shares", Page: 1}, 0},
	}

	for name, c := range cases {
		got, err := s.ListAdminPosts(ctx, c.filter)
		if err != nil || got.Total != c.want || len(got.Posts) != c.want {
			t.Errorf("%s: %d/%d, %v", name, len(got.Posts), got.Total, err)
		}
	}

	page2, err := s.ListAdminPosts(ctx, domain.AdminPostFilter{Page: 2})
	if err != nil || page2.Posts == nil || len(page2.Posts) != 0 || page2.Total != 3 {
		t.Errorf("page 2: %+v, %v", page2, err)
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
	id := seedPost(t, db, sourceID, domainID, "https://a.example/1", "en", "One", []string{"proposed-x"}, "0", "0")

	s := NewPostStore(db)

	p, err := s.GetAdminPost(ctx, id)
	if err != nil || p.SourceURL != "https://a.example/feed" || p.Content != "body" || len(p.Tags) != 1 {
		t.Errorf("got %+v, %v", p, err)
	}

	for _, bad := range []string{"00000000-0000-0000-0000-000000000000", "x"} {
		if _, err := s.GetAdminPost(ctx, bad); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}
