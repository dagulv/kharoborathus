package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSourceRoutes(t *testing.T) {
	mux, _, _ := newMux(t)

	valid := `{"url":"https://example.com/","adapter":"rss","listingUrl":"https://example.com/feed","rate":15,"lang":""}`

	if rec := do(mux, http.MethodPost, "/admin/sources", valid); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	cases := []struct {
		name, body string
		want       int
		message    string
	}{
		{"duplicate", valid, http.StatusConflict, "A source with this feed or page already exists"},
		{"no url", `{"url":" ","adapter":"rss","listingUrl":"https://example.com/x","rate":15,"lang":""}`, http.StatusBadRequest, "URL is required"},
		{"bad url", `{"url":"not a url","adapter":"rss","listingUrl":"https://example.com/x","rate":15,"lang":""}`, http.StatusBadRequest, "Must be a valid URL"},
		{"ftp", `{"url":"ftp://x","adapter":"rss","listingUrl":"https://example.com/x","rate":15,"lang":""}`, http.StatusBadRequest, "Only http(s) URLs are supported"},
		{"no listing", `{"url":"https://example.com/","adapter":"","listingUrl":"","rate":15,"lang":""}`, http.StatusBadRequest, "Check the URL first"},
		{"zero rate", `{"url":"https://example.com/","adapter":"page","listingUrl":"https://example.com/x","rate":0,"lang":""}`, http.StatusBadRequest, "Rate must be at least 1"},
		{"lang", `{"url":"https://example.com/","adapter":"page","listingUrl":"https://example.com/x","rate":5,"lang":"de"}`, http.StatusBadRequest, "Unknown language"},
		{"bad json", `{`, http.StatusBadRequest, ""},
	}

	for _, c := range cases {
		rec := do(mux, http.MethodPost, "/admin/sources", c.body)
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.message) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
	}

	rec := do(mux, http.MethodGet, "/admin/sources", "")
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK || len(list) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	for _, key := range []string{"id", "domainUrl", "postCount", "lastPostedAt", "deletedAt", "status", "enabled"} {
		if _, ok := list[0][key]; !ok {
			t.Errorf("list: no %s in %v", key, list[0])
		}
	}

	id := list[0]["id"].(string)

	if rec := do(mux, http.MethodPost, "/admin/sources/"+id+"/review", `{"enabled":false}`); rec.Code != http.StatusNoContent {
		t.Errorf("review: %d %s", rec.Code, rec.Body)
	}

	if rec := do(mux, http.MethodPost, "/admin/sources/00000000-0000-0000-0000-000000000000/review", `{"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("review unknown: %d", rec.Code)
	}

	if rec := do(mux, http.MethodGet, "/admin/sources?deleted=true", ""); rec.Code != http.StatusOK {
		t.Errorf("deleted: %d", rec.Code)
	}
}

func TestStatsRoute(t *testing.T) {
	mux, _, _ := newMux(t)

	rec := do(mux, http.MethodGet, "/admin/stats", "")

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	for _, key := range []string{"sources", "enabledSources", "flaggedSources", "posts", "postsToday", "searchesToday", "links"} {
		if _, ok := got[key]; !ok {
			t.Errorf("no %s in %v", key, got)
		}
	}
}

func TestSourceRoutesQueueBackfill(t *testing.T) {
	mux, db, backfills := newMux(t)
	ctx := context.Background()

	body := `{"url":"https://example.com/","adapter":"rss","listingUrl":"https://example.com/feed","rate":15,"lang":""}`
	if rec := do(mux, http.MethodPost, "/admin/sources", body); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	select {
	case s := <-backfills:
		if s.URL != "https://example.com/feed" || !s.Enabled {
			t.Errorf("queued %+v", s)
		}
	default:
		t.Fatal("create queued no backfill")
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO sources (domain_id, adapter, url, enabled, status)
		SELECT domain_id, 'rss', v.url, false, v.status
		FROM (SELECT domain_id FROM sources LIMIT 1) d,
			(VALUES ('https://example.com/p', 'proposed'), ('https://example.com/f', 'flagged')) v (url, status)
		`); err != nil {
		t.Fatal(err)
	}

	var proposed, flagged string
	if err := db.QueryRow(ctx, `SELECT id FROM sources WHERE url = 'https://example.com/p'`).Scan(&proposed); err != nil {
		t.Fatal(err)
	}

	if err := db.QueryRow(ctx, `SELECT id FROM sources WHERE url = 'https://example.com/f'`).Scan(&flagged); err != nil {
		t.Fatal(err)
	}

	// Keeping a flagged source is not its first scrape, so it queues nothing.
	if rec := do(mux, http.MethodPost, "/admin/sources/"+flagged+"/review", `{"enabled":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("keep: %d %s", rec.Code, rec.Body)
	}

	select {
	case s := <-backfills:
		t.Errorf("keep queued %s", s.URL)
	default:
	}

	if rec := do(mux, http.MethodPost, "/admin/sources/"+proposed+"/review", `{"enabled":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}

	select {
	case s := <-backfills:
		if s.ID != proposed {
			t.Errorf("approve queued %s", s.ID)
		}
	default:
		t.Error("approve queued no backfill")
	}
}
