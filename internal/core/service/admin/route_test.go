package admin

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

func newMux(t *testing.T) (*http.ServeMux, *database.Database, chan domain.Source) {
	mux, db, backfills, _ := newMuxResyncs(t)
	return mux, db, backfills
}

func newMuxResyncs(t *testing.T) (*http.ServeMux, *database.Database, chan domain.Source, chan domain.Resync) {
	t.Helper()

	return newMuxOn(t, dbtest.New(t))
}

func newMuxOn(t *testing.T, db *database.Database) (*http.ServeMux, *database.Database, chan domain.Source, chan domain.Resync) {
	t.Helper()

	mux, backfills, resyncs, _ := newMuxQueues(t, db)

	return mux, db, backfills, resyncs
}

func newMuxQueues(t *testing.T, db *database.Database) (*http.ServeMux, chan domain.Source, chan domain.Resync, chan string) {
	t.Helper()

	stats := postgres.NewStatsStore(db)
	sources := postgres.NewSourceStore(db)
	posts := postgres.NewPostStore(db)
	tags := postgres.NewTagStore(db)
	logs := postgres.NewLogStore(db)
	traces := postgres.NewTraceStore(db)
	categories := postgres.NewCategoryStore(db)
	entities := postgres.NewEntityStore(db)

	backfills := make(chan domain.Source, 4)
	resyncs := make(chan domain.Resync, 1)
	searchIDs := make(chan string, 4)

	mux := http.NewServeMux()
	Routes(mux, backfills, resyncs, searchIDs, Stores{Stats: &stats, Sources: &sources, Posts: &posts, Tags: &tags, Logs: &logs, Traces: &traces, Categories: &categories, Entities: &entities}, slog.New(slog.DiscardHandler))

	return mux, backfills, resyncs, searchIDs
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
