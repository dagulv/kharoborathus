package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

func TestCategoryRoutes(t *testing.T) {
	mux, db, _, _ := newMuxOn(t, dbtest.Seeded(t))
	ctx := context.Background()

	if _, err := db.Exec(ctx, `INSERT INTO tags (key) VALUES ('acquisition') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		method, path, body string
		want               int
		message            string
	}{
		{http.MethodPost, "/admin/categories", `{"key":"Space Race","sv":" rymd ","en":"space"}`, http.StatusCreated, ""},
		{http.MethodPost, "/admin/categories", `{"key":"space_race","sv":"rymd","en":"space"}`, http.StatusConflict, "Category already exists"},
		{http.MethodPost, "/admin/categories", `{"key":"Real Estate","sv":"fastigheter","en":"real estate"}`, http.StatusConflict, "Category already exists"},
		{http.MethodPost, "/admin/categories", `{"key":"x","sv":"x"}`, http.StatusBadRequest, ""},
		{http.MethodPost, "/admin/categories", `{"key":"x","sv":"x","en":"  "}`, http.StatusBadRequest, ""},
		{http.MethodPost, "/admin/categories", `{"key":"x","sv":"` + strings.Repeat("s", 101) + `","en":"x"}`, http.StatusBadRequest, ""},
		{http.MethodPost, "/admin/categories", `{"key":"  ","sv":"x","en":"x"}`, http.StatusBadRequest, ""},
		{http.MethodPost, "/admin/categories", `{"key":"` + strings.Repeat("k", 41) + `","sv":"x","en":"x"}`, http.StatusBadRequest, ""},
		{http.MethodPost, "/admin/categories", `{"key":"x","sv":"x","en":"x","extra":1}`, http.StatusBadRequest, ""},
		{http.MethodPatch, "/admin/categories/space_race", `{"enabled":false}`, http.StatusNoContent, ""},
		{http.MethodPatch, "/admin/categories/nope", `{"enabled":false}`, http.StatusNotFound, "Unknown category"},
		{http.MethodPatch, "/admin/categories/" + strings.Repeat("k", 41), `{"enabled":false}`, http.StatusBadRequest, ""},
		{http.MethodPut, "/admin/tags/acquisition/categories", `{"categories":["Space Race","finance"]}`, http.StatusNoContent, ""},
		{http.MethodPut, "/admin/tags/acquisition/categories", `{"categories":["nope"]}`, http.StatusBadRequest, "Unknown category"},
		{http.MethodPut, "/admin/tags/nope/categories", `{"categories":["finance"]}`, http.StatusNotFound, "Unknown tag nope"},
		{http.MethodPut, "/admin/tags/acquisition/categories", `{}`, http.StatusBadRequest, ""},
		{http.MethodPut, "/admin/tags/" + strings.Repeat("k", 41) + "/categories", `{"categories":[]}`, http.StatusBadRequest, ""},
	}

	for _, c := range cases {
		rec := do(mux, c.method, c.path, c.body)
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.message) {
			t.Errorf("%s %s %s: %d %s", c.method, c.path, c.body, rec.Code, rec.Body)
		}
	}

	rec := do(mux, http.MethodGet, "/admin/categories", "")

	var got []struct {
		Key       string            `json:"key"`
		Enabled   bool              `json:"enabled"`
		Terms     map[string]string `json:"terms"`
		TagCount  int               `json:"tagCount"`
		PostCount int               `json:"postCount"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	byKey := map[string]int{}
	for i, c := range got {
		byKey[c.Key] = i
	}

	space, ok := byKey["space_race"]
	if !ok || got[space].Enabled || got[space].Terms["sv"] != "rymd" || got[space].TagCount != 1 {
		t.Errorf("space_race: %+v", got)
	}

	if _, ok := byKey["real_estate"]; !ok || len(got) != 19 {
		t.Errorf("seed categories missing: %d", len(got))
	}

	rec = do(mux, http.MethodGet, "/admin/tags", "")

	var overview struct {
		Tags []struct {
			Key        string   `json:"key"`
			Categories []string `json:"categories"`
		} `json:"tags"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}

	for _, tag := range overview.Tags {
		if tag.Key == "acquisition" && strings.Join(tag.Categories, ",") != "finance,space_race" {
			t.Errorf("acquisition categories: %v", tag.Categories)
		}
	}
}

func TestListPostsCategoryFilter(t *testing.T) {
	mux, _, _ := newMux(t)

	if rec := do(mux, http.MethodGet, "/admin/posts?category="+strings.Repeat("k", 201), ""); rec.Code != http.StatusBadRequest {
		t.Errorf("long category: %d %s", rec.Code, rec.Body)
	}

	if rec := do(mux, http.MethodGet, "/admin/posts?category=nope", ""); rec.Code != http.StatusOK {
		t.Errorf("unknown category: %d %s", rec.Code, rec.Body)
	}
}
