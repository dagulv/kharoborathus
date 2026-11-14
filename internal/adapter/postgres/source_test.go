package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/jackc/pgx/v5"
)

func TestListAdminSources(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	domainID, live := seedSource(t, db, "https://a.example", "https://a.example/feed")
	_, deleted := seedSource(t, db, "https://a.example", "https://a.example/old")

	if _, err := db.Exec(ctx, `UPDATE sources SET deleted_at = now() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}

	seedPost(t, db, live, domainID, "https://a.example/1", "en", "One", nil, "1 day", "0")
	seedPost(t, db, live, domainID, "https://a.example/2", "en", "Two", nil, "3 days", "0")

	s := NewSourceStore(db)

	got, err := s.ListAdminSources(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].ID != live || got[0].DomainURL != "https://a.example" || got[0].PostCount != 2 || got[0].LastPostedAt == nil || got[0].DeletedAt != nil {
		t.Fatalf("got %+v", got)
	}

	all, err := s.ListAdminSources(ctx, true)
	if err != nil || len(all) != 2 {
		t.Fatalf("with deleted: %d, %v", len(all), err)
	}

	// Newest first.
	if all[0].ID != deleted || all[0].DeletedAt == nil || all[1].PostCount != 2 {
		t.Errorf("with deleted: %+v", all)
	}
}

func TestCreateSource(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	s := NewSourceStore(db)

	in := domain.NewSource{URL: "https://www.Example.com/news", Adapter: "rss", ListingURL: "https://example.com/feed", Rate: 15, Lang: "sv"}

	if err := s.CreateSource(ctx, in); err != nil {
		t.Fatal(err)
	}

	var base, adapter, lang string
	var enabled bool
	var rate int
	if err := db.QueryRow(ctx, `
		SELECT d.base_url, s.adapter, s.enabled, s.rate, s.lang FROM sources s JOIN domains d ON d.id = s.domain_id WHERE s.url = $1
		`, in.ListingURL).Scan(&base, &adapter, &enabled, &rate, &lang); err != nil {
		t.Fatal(err)
	}

	if base != "https://example.com" || adapter != "rss" || !enabled || rate != 15 || lang != "sv" {
		t.Errorf("got %s %s %v %d %s", base, adapter, enabled, rate, lang)
	}

	if err := s.CreateSource(ctx, in); !errors.Is(err, domain.ErrSourceExists) {
		t.Errorf("duplicate: %v", err)
	}

	if _, err := db.Exec(ctx, `UPDATE sources SET deleted_at = now(), enabled = false WHERE url = $1`, in.ListingURL); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateSource(ctx, in); err != nil {
		t.Errorf("after delete: %v", err)
	}
}

func TestReviewSource(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	s := NewSourceStore(db)

	_, keep := seedSource(t, db, "https://a.example", "https://a.example/feed")
	_, disable := seedSource(t, db, "https://a.example", "https://a.example/other")

	if _, err := db.Exec(ctx, `UPDATE sources SET enabled = true, status = 'flagged'`); err != nil {
		t.Fatal(err)
	}

	if err := s.ReviewSource(ctx, keep, true); err != nil {
		t.Fatal(err)
	}

	if err := s.ReviewSource(ctx, disable, false); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string]struct {
		status  string
		enabled bool
	}{keep: {"active", true}, disable: {"flagged", false}} {
		var status string
		var enabled bool
		if err := db.QueryRow(ctx, `SELECT status, enabled FROM sources WHERE id = $1`, id).Scan(&status, &enabled); err != nil {
			t.Fatal(err)
		}

		if status != want.status || enabled != want.enabled {
			t.Errorf("%s: %s %v", id, status, enabled)
		}
	}

	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "nope"} {
		if err := s.ReviewSource(ctx, id, true); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s: %v", id, err)
		}
	}
}
