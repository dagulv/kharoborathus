package admin

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

func newMux(t *testing.T) (*http.ServeMux, *database.Database) {
	t.Helper()

	db := dbtest.New(t)
	stats := postgres.NewStatsStore(db)
	sources := postgres.NewSourceStore(db)
	posts := postgres.NewPostStore(db)

	mux := http.NewServeMux()
	Routes(mux, Stores{Stats: &stats, Sources: &sources, Posts: &posts}, slog.New(slog.DiscardHandler))

	return mux, db
}

func do(mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	return rec
}
