package entities

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

type fakeStore struct {
	entities []domain.Entity
	filter   domain.EntityFilter
	key      string
	err      error
}

func (f *fakeStore) ListPublic(_ context.Context, filter domain.EntityFilter) ([]domain.Entity, error) {
	f.filter = filter

	return f.entities, f.err
}

func (f *fakeStore) GetPublic(_ context.Context, key string) (domain.Entity, error) {
	f.key = key

	if f.err != nil {
		return domain.Entity{}, f.err
	}

	if len(f.entities) == 0 {
		return domain.Entity{}, pgx.ErrNoRows
	}

	return f.entities[0], nil
}

func serve(s Store, target string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	Routes(mux, s, slog.New(slog.DiscardHandler))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))

	return w
}

var volvo = domain.Entity{Key: "volvo_cars", Name: "Volvo Cars", Kind: "company", Status: "active", Enabled: true, Terms: map[string][]string{"sv": {"Volvo Cars"}}, Tags: []string{"cars"}, Strength: 4}

func TestEntitiesRoutes(t *testing.T) {
	s := &fakeStore{entities: []domain.Entity{volvo}}

	w := serve(s, "/entities?kind=company&tag=cars&category=industry&limit=10&offset=5")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", w.Code, w.Header().Get("Content-Type"))
	}

	var list []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("body %s, %v", w.Body, err)
	}

	terms, _ := list[0]["terms"].(map[string]any)
	if list[0]["key"] != "volvo_cars" || list[0]["kind"] != "company" || list[0]["strength"] != float64(4) || list[0]["createdAt"] == nil || terms["sv"] == nil {
		t.Errorf("body %s", w.Body)
	}

	f := s.filter
	if f.Kind != "company" || f.Tag != "cars" || f.Category != "industry" || f.Limit != "10" || f.Offset != "5" {
		t.Errorf("filter %+v", f)
	}

	w = serve(s, "/entities/volvo_car_corporation")

	var one map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &one); err != nil || w.Code != http.StatusOK || one["key"] != "volvo_cars" || one["name"] != "Volvo Cars" || s.key != "volvo_car_corporation" {
		t.Errorf("status %d, body %s, key %q, %v", w.Code, w.Body, s.key, err)
	}
}

func TestEntitiesRoutesEmpty(t *testing.T) {
	w := serve(&fakeStore{}, "/entities")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("status %d, body %q", w.Code, w.Body)
	}
}

func TestEntitiesRoutesNotFound(t *testing.T) {
	if w := serve(&fakeStore{}, "/entities/nope"); w.Code != http.StatusNotFound {
		t.Errorf("status %d", w.Code)
	}
}

func TestEntitiesRoutesError(t *testing.T) {
	for _, target := range []string{"/entities", "/entities/volvo_cars"} {
		w := serve(&fakeStore{err: errors.New("boom")}, target)
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "boom") {
			t.Errorf("%s: status %d, body %q", target, w.Code, w.Body)
		}
	}
}
