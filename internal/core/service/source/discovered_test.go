package source

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/pace"
)

type discoveredExtractor struct {
	fakeExtractor
	postedAt time.Time
}

func (f *discoveredExtractor) Content(context.Context, *slog.Logger, domain.Domain, string, []byte) (string, error) {
	return "<p>" + article + "</p>", nil
}

func (f *discoveredExtractor) PostedAt(context.Context, *slog.Logger, domain.Domain, string, []byte) (time.Time, error) {
	return f.postedAt, nil
}

type discoveredFixture struct {
	d        Discovered
	db       *database.Database
	ex       *discoveredExtractor
	enriched chan string
	fromPost string
}

func discoveredSetup(t *testing.T) discoveredFixture {
	s, base, db := qualityServiceDB(t, &fakeClassifier{score: 0.9})
	ex := &discoveredExtractor{postedAt: time.Now()}
	s.Extractor = ex
	enriched := make(chan string, 10)
	s.Enriched = enriched

	f := discoveredFixture{db: db, ex: ex, enriched: enriched}

	if err := db.QueryRow(context.Background(), `
		INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content)
		VALUES ($1, 'https://a.example/from', 'sv', $2, 't', 's', 'c') RETURNING id
		`, base.SourceID, base.DomainID).Scan(&f.fromPost); err != nil {
		t.Fatal(err)
	}

	store := postgres.NewDiscoveredStore(db)
	f.d = Discovered{Service: s, Store: &store, Client: &http.Client{}, Pacer: pace.New(0), Calls: slog.New(slog.DiscardHandler)}

	return f
}

func (f discoveredFixture) queue(t *testing.T, url string) string {
	t.Helper()

	var id string
	if err := f.db.QueryRow(context.Background(), `INSERT INTO discovery_posts (url, from_post_id) VALUES ($1, $2) RETURNING id`, url, f.fromPost).Scan(&id); err != nil {
		t.Fatal(err)
	}

	return id
}

func (f discoveredFixture) status(t *testing.T, id string) string {
	t.Helper()

	var status string
	if err := f.db.QueryRow(context.Background(), `SELECT status FROM discovery_posts WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}

	return status
}

func (f discoveredFixture) count(t *testing.T, sql string, args ...any) (n int) {
	t.Helper()

	if err := f.db.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

func pageServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html lang="sv-SE"><body><p>` + article + `</p></body></html>`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestIngestNewDomain(t *testing.T) {
	f := discoveredSetup(t)
	srv := pageServer(t)
	id := f.queue(t, srv.URL+"/a")

	if err := f.d.ingest(context.Background(), slog.New(slog.DiscardHandler), id); err != nil {
		t.Fatal(err)
	}

	if s := f.status(t, id); s != domain.StatusAccepted {
		t.Errorf("status %q", s)
	}

	if n := f.count(t, `SELECT count(*) FROM raw_posts r JOIN sources s ON s.id = r.source_id WHERE r.url = $1 AND r.status = 'accepted' AND r.lang = 'sv' AND s.status = 'proposed'`, srv.URL+"/a"); n != 1 {
		t.Errorf("raw posts %d", n)
	}

	if n := f.count(t, `SELECT count(*) FROM source_discovered_by_post WHERE post_id = $1`, f.fromPost); n != 1 {
		t.Errorf("discovered by post %d", n)
	}

	if len(f.enriched) != 1 {
		t.Errorf("sent %d ids", len(f.enriched))
	}
}

func TestIngestRedirect(t *testing.T) {
	f := discoveredSetup(t)
	target := pageServer(t)
	srv := httptest.NewServer(http.RedirectHandler(target.URL+"/final", http.StatusFound))
	t.Cleanup(srv.Close)

	id := f.queue(t, srv.URL+"/short")

	if err := f.d.ingest(context.Background(), slog.New(slog.DiscardHandler), id); err != nil {
		t.Fatal(err)
	}

	if n := f.count(t, `SELECT count(*) FROM raw_posts r JOIN sources s ON s.id = r.source_id WHERE r.url = $1 AND s.url = $2`, target.URL+"/final", target.URL); n != 1 {
		t.Errorf("raw posts at final url %d", n)
	}
}

func TestIngestRedirectToBlocked(t *testing.T) {
	f := discoveredSetup(t)
	target := pageServer(t)
	srv := httptest.NewServer(http.RedirectHandler(target.URL+"/final", http.StatusFound))
	t.Cleanup(srv.Close)

	ctx := context.Background()

	d, err := f.d.Service.DomainStore.GetOrCreateDomain(ctx, target.URL)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.db.Exec(ctx, `INSERT INTO sources (domain_id, adapter, url, enabled) VALUES ($1, 'rss', $2, false)`, d.ID, target.URL+"/feed"); err != nil {
		t.Fatal(err)
	}

	id := f.queue(t, srv.URL+"/short")

	if err := f.d.ingest(ctx, slog.New(slog.DiscardHandler), id); err != nil {
		t.Fatal(err)
	}

	if s := f.status(t, id); s != domain.StatusFailed {
		t.Errorf("status %q", s)
	}

	if n := f.count(t, `SELECT count(*) FROM raw_posts WHERE url = $1`, target.URL+"/final"); n != 0 {
		t.Errorf("raw posts %d", n)
	}
}

func TestIngestFinalStatuses(t *testing.T) {
	cases := map[string]struct {
		path     string
		postedAt time.Time
		want     string
	}{
		"stale":   {path: "/old", postedAt: time.Now().AddDate(0, 0, -8), want: domain.StatusRejected},
		"undated": {path: "/undated", want: domain.StatusAccepted},
		"missing": {path: "/missing", postedAt: time.Now(), want: domain.StatusFailed},
	}

	for name, c := range cases {
		f := discoveredSetup(t)
		f.ex.postedAt = c.postedAt
		srv := pageServer(t)
		id := f.queue(t, srv.URL+c.path)

		if err := f.d.ingest(context.Background(), slog.New(slog.DiscardHandler), id); err != nil {
			t.Fatal(err)
		}

		if s := f.status(t, id); s != c.want {
			t.Errorf("%s: status %q", name, s)
		}
	}
}

func TestIngestNoDailyCap(t *testing.T) {
	f := discoveredSetup(t)
	ctx := context.Background()

	if _, err := f.db.Exec(ctx, `
		INSERT INTO discovery_posts (url, from_post_id, status, checked_at)
		SELECT 'https://b.example/' || i, $1, 'rejected', now() FROM generate_series(1, 200) AS i
		`, f.fromPost); err != nil {
		t.Fatal(err)
	}

	id := f.queue(t, pageServer(t).URL+"/a")

	if err := f.d.ingest(ctx, slog.New(slog.DiscardHandler), id); err != nil {
		t.Fatal(err)
	}

	if s := f.status(t, id); s != domain.StatusAccepted {
		t.Errorf("status %q", s)
	}
}

func TestIngestCatchUp(t *testing.T) {
	f := discoveredSetup(t)
	srv := pageServer(t)
	ctx := context.Background()

	fresh := f.queue(t, srv.URL+"/fresh")
	old := f.queue(t, srv.URL+"/old")

	if _, err := f.db.Exec(ctx, `UPDATE discovery_posts SET created_at = now() - interval '8 days' WHERE id = $1`, old); err != nil {
		t.Fatal(err)
	}

	if err := f.d.catchUp(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}

	if s := f.status(t, fresh); s != domain.StatusAccepted {
		t.Errorf("fresh %q", s)
	}

	if s := f.status(t, old); s != domain.StatusPending {
		t.Errorf("old %q", s)
	}
}
