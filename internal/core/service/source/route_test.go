package source

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

type fakeProber struct {
	listing domain.Listing
	err     error
}

func (f fakeProber) Probe(context.Context, string) (domain.Listing, error) {
	return f.listing, f.err
}

func TestListingRoute(t *testing.T) {
	get := func(p fakeProber, target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		listingRoute(p, slog.New(slog.DiscardHandler))(rec, httptest.NewRequest(http.MethodGet, target, nil))

		return rec
	}

	listing := domain.Listing{
		Candidates: []domain.Candidate{{Adapter: "page", URL: "https://a.example/"}},
		Archive:    &domain.Archive{URL: "https://a.example/wp-json/wp/v2/posts", Total: 420},
	}

	want := `{"candidates":[{"adapter":"page","url":"https://a.example/"}],"archive":{"url":"https://a.example/wp-json/wp/v2/posts","total":420}}`
	if rec := get(fakeProber{listing: listing}, "/listing?url=https%3A%2F%2Fa.example%2F"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("ok: %d %s", rec.Code, rec.Body)
	}

	if rec := get(fakeProber{err: errors.New("down")}, "/listing?url=https%3A%2F%2Fa.example%2F"); rec.Code != http.StatusBadGateway {
		t.Errorf("unreachable: %d", rec.Code)
	}

	if rec := get(fakeProber{}, "/listing"); rec.Code != http.StatusBadRequest {
		t.Errorf("no url: %d", rec.Code)
	}
}

func TestQueueSource(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	var sourceID string
	if err := db.QueryRow(ctx, `
		WITH d AS (INSERT INTO domains (base_url) VALUES ('https://a.example') RETURNING id)
		INSERT INTO sources (domain_id, adapter, url) SELECT id, 'rss', 'https://a.example/feed' FROM d RETURNING id
		`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}

	store := postgres.NewSourceStore(db)

	for _, name := range []string{"sync", "backfill"} {
		if _, err := db.Exec(ctx, `UPDATE sources SET enabled = false WHERE id = $1`, sourceID); err != nil {
			t.Fatal(err)
		}

		queue := make(chan domain.Source, 1)
		handler := queueSource(&store, queue, name, slog.New(slog.DiscardHandler))

		send := func(id string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/sources/"+id+"/"+name, nil)
			req.SetPathValue("id", id)
			handler(rec, req)

			return rec
		}

		if rec := send(sourceID); rec.Code != http.StatusConflict {
			t.Errorf("%s disabled: %d", name, rec.Code)
		}

		if _, err := db.Exec(ctx, `UPDATE sources SET enabled = true WHERE id = $1`, sourceID); err != nil {
			t.Fatal(err)
		}

		if rec := send(sourceID); rec.Code != http.StatusAccepted || len(queue) != 1 {
			t.Errorf("%s active: %d, queued %d", name, rec.Code, len(queue))
		}

		if rec := send(sourceID); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), name+" queue full") {
			t.Errorf("%s queue full: %d %q", name, rec.Code, rec.Body.String())
		}

		for _, id := range []string{"00000000-0000-0000-0000-000000000000", "nope"} {
			if rec := send(id); rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d", name, id, rec.Code)
			}
		}
	}
}

func TestRoutesUnderAdmin(t *testing.T) {
	store := postgres.NewSourceStore(dbtest.New(t))
	mux := http.NewServeMux()
	Routes(mux, &store, make(chan domain.Source), make(chan domain.Source), fakeProber{}, slog.New(slog.DiscardHandler))

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/admin/listing", http.StatusBadRequest},
		{http.MethodPost, "/admin/sources/nope/sync", http.StatusNotFound},
		{http.MethodPost, "/admin/sources/nope/backfill", http.StatusNotFound},
		{http.MethodGet, "/listing", http.StatusNotFound},
	}

	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))

		if rec.Code != c.want {
			t.Errorf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
}
