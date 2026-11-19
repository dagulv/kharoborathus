package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

func TestEntityRoutes(t *testing.T) {
	db := dbtest.New(t)
	mux, _, _, searchIDs := newMuxQueues(t, db)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `
		INSERT INTO domains (base_url) VALUES ('https://a.example');
		INSERT INTO sources (domain_id, adapter, url, enabled) SELECT id, 'rss', 'https://a.example/feed', true FROM domains;
		INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content) SELECT id, domain_id, 'https://a.example/1', 'en', 't', 's', 'c' FROM sources;
		INSERT INTO raw_posts (source_id, domain_id, url, lang, status) SELECT id, domain_id, 'https://a.example/1', 'en', 'accepted' FROM sources;
		INSERT INTO tags (key) VALUES ('batteries');
		INSERT INTO entities (key, name, kind) VALUES ('volvo_cars', 'Volvo Cars', 'company'), ('northvolt', 'Northvolt', 'company'), ('other', 'Other', 'company');
		INSERT INTO entity_aliases (alias, key) VALUES ('vcc', 'volvo_cars');
		INSERT INTO post_entities (post_id, entity) SELECT id, 'volvo_cars' FROM posts;
		`); err != nil {
		t.Fatal(err)
	}

	long := strings.Repeat("k", 201)

	cases := []struct {
		method, path, body string
		want               int
		message            string
	}{
		{http.MethodGet, "/admin/entities?kind=person", "", http.StatusBadRequest, "Unknown kind"},
		{http.MethodGet, "/admin/entities?status=nope", "", http.StatusBadRequest, ""},
		{http.MethodGet, "/admin/entities?focus=maybe", "", http.StatusBadRequest, ""},
		{http.MethodGet, "/admin/entities?page=0", "", http.StatusBadRequest, ""},
		{http.MethodGet, "/admin/entities?search=" + strings.Repeat("s", 501), "", http.StatusBadRequest, ""},
		{http.MethodGet, "/admin/entities/nope", "", http.StatusNotFound, "Unknown entity nope"},
		{http.MethodGet, "/admin/entities/" + long, "", http.StatusBadRequest, ""},
		{http.MethodPatch, "/admin/entities/volvo_cars", `{"kind":"person"}`, http.StatusBadRequest, "Unknown kind"},
		{http.MethodPatch, "/admin/entities/volvo_cars", `{"name":" "}`, http.StatusBadRequest, "Name must be 1 to 200 characters"},
		{http.MethodPatch, "/admin/entities/volvo_cars", `{"name":"` + long + `"}`, http.StatusBadRequest, "Name must be 1 to 200 characters"},
		{http.MethodPatch, "/admin/entities/volvo_cars", `{"extra":1}`, http.StatusBadRequest, ""},
		{http.MethodPatch, "/admin/entities/nope", `{"enabled":false}`, http.StatusNotFound, "Unknown entity nope"},
		{http.MethodPatch, "/admin/entities/" + long, `{"enabled":false}`, http.StatusBadRequest, ""},
		{http.MethodPatch, "/admin/entities/northvolt", `{"kind":"Technology","name":"Northvolt AB","enabled":false}`, http.StatusNoContent, ""},
		{http.MethodPut, "/admin/entities/volvo_cars/tags", `{"tags":["batteries","nope"]}`, http.StatusBadRequest, "Unknown tag"},
		{http.MethodPut, "/admin/entities/nope/tags", `{"tags":["batteries"]}`, http.StatusNotFound, "Unknown entity nope"},
		{http.MethodPut, "/admin/entities/volvo_cars/tags", `{}`, http.StatusBadRequest, ""},
		{http.MethodPut, "/admin/entities/" + long + "/tags", `{"tags":[]}`, http.StatusBadRequest, ""},
		{http.MethodPut, "/admin/entities/volvo_cars/tags", `{"tags":["Batteries"]}`, http.StatusNoContent, ""},
		{http.MethodPost, "/admin/entities/merge", `{"from":"other","to":"other"}`, http.StatusBadRequest, "Cannot merge an entity into itself"},
		{http.MethodPost, "/admin/entities/merge", `{"from":"other","to":"nope"}`, http.StatusNotFound, "Unknown entity"},
		{http.MethodPost, "/admin/entities/merge", `{"from":"","to":"other"}`, http.StatusBadRequest, ""},
		{http.MethodPost, "/admin/entities/merge", `{"from":"other","to":"northvolt"}`, http.StatusNoContent, ""},
		{http.MethodGet, "/admin/posts?entity=" + long, "", http.StatusBadRequest, ""},
	}

	for _, c := range cases {
		rec := do(mux, c.method, c.path, c.body)
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.message) {
			t.Errorf("%s %s %s: %d %s", c.method, c.path, c.body, rec.Code, rec.Body)
		}
	}

	if len(searchIDs) != 0 {
		t.Fatalf("queued %d before focus", len(searchIDs))
	}

	if rec := do(mux, http.MethodPatch, "/admin/entities/volvo_cars", `{"focus":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("focus: %d %s", rec.Code, rec.Body)
	}

	var queued string
	if err := db.QueryRow(ctx, `SELECT id FROM discovery_search_queries WHERE normalized_entity = 'volvo_cars' AND action = 'depth'`).Scan(&queued); err != nil {
		t.Fatal(err)
	}

	if len(searchIDs) != 1 || <-searchIDs != queued {
		t.Errorf("focus did not enqueue %s", queued)
	}

	rec := do(mux, http.MethodGet, "/admin/entities/volvo_cars", "")

	var got struct {
		Key     string   `json:"key"`
		Focus   bool     `json:"focus"`
		Tags    []string `json:"tags"`
		Aliases []string `json:"aliases"`
		Support int      `json:"support"`
		Queries []struct {
			ID string `json:"id"`
		} `json:"queries"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	if got.Key != "volvo_cars" || !got.Focus || strings.Join(got.Tags, ",") != "batteries" || strings.Join(got.Aliases, ",") != "vcc" || got.Support != 1 || len(got.Queries) != 1 || got.Queries[0].ID != queued {
		t.Errorf("get: %+v", got)
	}

	rec = do(mux, http.MethodGet, "/admin/entities?kind=company&focus=true", "")

	var page struct {
		Entities []struct {
			Key string `json:"key"`
		} `json:"entities"`
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"pageSize"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if len(page.Entities) != 1 || page.Entities[0].Key != "volvo_cars" || page.Total != 1 || page.Page != 1 || page.PageSize != 50 {
		t.Errorf("list: %+v", page)
	}

	var northvolt int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM entities WHERE key = 'northvolt' AND kind = 'technology' AND name = 'Northvolt AB' AND NOT enabled`).Scan(&northvolt); err != nil || northvolt != 1 {
		t.Errorf("northvolt patch: %d, %v", northvolt, err)
	}

	if rec := do(mux, http.MethodGet, "/admin/posts?entity=volvo_cars", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Errorf("posts by entity: %d %s", rec.Code, rec.Body)
	}
}
