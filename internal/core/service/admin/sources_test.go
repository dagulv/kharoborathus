package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSourceRoutes(t *testing.T) {
	mux, _ := newMux(t)

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
	mux, _ := newMux(t)

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
