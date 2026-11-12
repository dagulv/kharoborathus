package source

import (
	"context"
	"database/sql"
	"iter"
	"log/slog"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/lib/iterx"
)

type fakeLister struct {
	sources []domain.Source
	filters chan domain.SourceFilter
}

func (f fakeLister) ListSources(_ context.Context, filter domain.SourceFilter) iter.Seq2[domain.Source, error] {
	f.filters <- filter

	return func(yield func(domain.Source, error) bool) {
		for _, s := range f.sources {
			if !yield(s, nil) {
				return
			}
		}
	}
}

func TestBackfillRuled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lister := fakeLister{sources: []domain.Source{{ID: "a"}, {ID: "b"}}, filters: make(chan domain.SourceFilter, 2)}
	ruled := make(chan string)
	backfills := make(chan domain.Source, 1)

	go BackfillRuled(ctx, lister, ruled, backfills, slog.New(slog.DiscardHandler))

	ruled <- "d"

	filter := <-lister.filters
	want := domain.SourceFilter{
		DomainID: sql.Null[string]{V: "d", Valid: true},
		Adapter:  sql.Null[string]{V: "page", Valid: true},
		Enabled:  sql.Null[bool]{V: true, Valid: true},
		Status:   sql.Null[string]{V: domain.StatusActive, Valid: true},
	}

	if filter.DomainID != want.DomainID || filter.Adapter != want.Adapter || filter.Enabled != want.Enabled || filter.Status != want.Status {
		t.Errorf("filter %+v", filter)
	}

	// ruled is unbuffered, so a second send lands only once d is done: b met the full queue and was dropped.
	select {
	case ruled <- "e":
	case <-time.After(2 * time.Second):
		t.Fatal("worker blocked on a full backfill queue")
	}

	if len(backfills) != 1 {
		t.Fatalf("queued %d", len(backfills))
	}

	if s := <-backfills; s.ID != "a" {
		t.Errorf("backfilled %s", s.ID)
	}
}

func TestListSourcesByDomainAndAdapter(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	var domainID string
	if err := db.QueryRow(ctx, `INSERT INTO domains (base_url) VALUES ('https://a.example') RETURNING id`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO sources (domain_id, adapter, url, enabled) VALUES
		($1, 'page', 'https://a.example/news', true),
		($1, 'rss', 'https://a.example/feed', true),
		($1, 'page', 'https://a.example/old', false)
		`, domainID); err != nil {
		t.Fatal(err)
	}

	store := postgres.NewSourceStore(db)

	got, err := iterx.Collect(store.ListSources(ctx, domain.SourceFilter{
		DomainID: sql.Null[string]{V: domainID, Valid: true},
		Adapter:  sql.Null[string]{V: "page", Valid: true},
		Enabled:  sql.Null[bool]{V: true, Valid: true},
		Status:   sql.Null[string]{V: domain.StatusActive, Valid: true},
	}))
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].URL != "https://a.example/news" {
		t.Errorf("got %+v", got)
	}
}
