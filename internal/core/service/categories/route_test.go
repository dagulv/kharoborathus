package categories

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
)

type fakeStore struct {
	categories []domain.Category
	err        error
}

func (f *fakeStore) EnabledCategories(context.Context) ([]domain.Category, error) {
	return f.categories, f.err
}

func serve(s Store) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	Routes(mux, s, slog.New(slog.DiscardHandler))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/categories", nil))

	return w
}

func TestCategoriesRoute(t *testing.T) {
	s := &fakeStore{categories: []domain.Category{{Key: "finance", Enabled: true, Terms: map[string]string{"sv": "finans"}, Tags: []string{"dividend"}}}}

	w := serve(s)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", w.Code, w.Header().Get("Content-Type"))
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 1 {
		t.Fatalf("body %s, %v", w.Body, err)
	}

	terms, _ := got[0]["terms"].(map[string]any)
	if got[0]["key"] != "finance" || got[0]["enabled"] != true || terms["sv"] != "finans" || got[0]["createdAt"] == nil {
		t.Errorf("body %s", w.Body)
	}
}

func TestCategoriesRouteEmpty(t *testing.T) {
	w := serve(&fakeStore{})
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("status %d, body %q", w.Code, w.Body)
	}
}

func TestCategoriesRouteError(t *testing.T) {
	w := serve(&fakeStore{err: errors.New("boom")})
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "boom") {
		t.Errorf("status %d, body %q", w.Code, w.Body)
	}
}
