package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
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

	if !slices.Equal(posts[1].Tags, []string{"acquisition"}) || posts[1].Title != volvo.Title || posts[1].Content != "c" {
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
	if err != nil || got.ID != volvo.ID || !slices.Equal(got.Tags, []string{"acquisition"}) {
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
