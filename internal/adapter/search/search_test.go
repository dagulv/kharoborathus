package discovery

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/jackc/pgx/v5"
)

type fakeStore struct {
	item     SearchItem
	pending  int
	used     int
	rejected []string
	saved    [][]SearchResult
	kept     [][]int
	urls     [][]string
	savedCtx error
}

func (s *fakeStore) GetQuery(context.Context, string) (SearchItem, error) { return s.item, nil }

func (s *fakeStore) PendingResults(context.Context, int) (int, error) { return s.pending, nil }

func (s *fakeStore) SearchesLastDay(context.Context) (int, error) { return s.used, nil }

func (s *fakeStore) MarkRejected(_ context.Context, id string) error {
	s.rejected = append(s.rejected, id)
	return nil
}

func (s *fakeStore) SaveResults(ctx context.Context, _ string, results []SearchResult) error {
	s.savedCtx = ctx.Err()
	s.saved = append(s.saved, results)
	return nil
}

func (s *fakeStore) SaveKept(_ context.Context, _ SearchItem, kept []int, urls []string) error {
	s.kept = append(s.kept, kept)
	s.urls = append(s.urls, urls)
	return nil
}

func (s *fakeStore) writes() int { return len(s.rejected) + len(s.saved) + len(s.kept) }

type fakeSearcher struct {
	results []SearchResult
	err     error
	calls   int
	cancel  context.CancelFunc
}

func (f *fakeSearcher) Search(context.Context, *slog.Logger, Query) ([]SearchResult, error) {
	f.calls++

	if f.cancel != nil {
		f.cancel()
	}

	return f.results, f.err
}

var closed = schedule.Schedule{Closed: []schedule.Window{{
	Days: []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday},
	To:   24 * time.Hour,
}}}

func testItem() SearchItem {
	return SearchItem{ID: "q", Entity: "volvo_cars", Action: "acquisition", Query: "Volvo Cars köper Northvolt", Lang: "sv", BaseURL: "https://own.example", FromPostID: "p"}
}

func runSearch(ctx context.Context, s *fakeStore, se *fakeSearcher, llm *fakeLLM, sched schedule.Schedule) error {
	l := slog.New(slog.DiscardHandler)
	return searchQuery(ctx, s, "q", se, NewResultFilter(llm), sched, l, l)
}

func TestSearchSkipsSearched(t *testing.T) {
	now := time.Now()
	s := &fakeStore{item: testItem()}
	s.item.SearchedAt = &now
	se := &fakeSearcher{}

	if err := runSearch(context.Background(), s, se, &fakeLLM{}, schedule.Always); err != nil || se.calls != 0 || s.writes() != 0 {
		t.Errorf("err %v, calls %d, writes %d", err, se.calls, s.writes())
	}
}

func TestSearchPendingCap(t *testing.T) {
	s := &fakeStore{item: testItem(), pending: 200}
	se := &fakeSearcher{}

	if err := runSearch(context.Background(), s, se, &fakeLLM{}, schedule.Always); err != nil || se.calls != 0 || s.writes() != 0 {
		t.Errorf("err %v, calls %d, writes %d", err, se.calls, s.writes())
	}
}

func TestSearchBudget(t *testing.T) {
	s := &fakeStore{item: testItem(), used: 322}
	se := &fakeSearcher{}

	if err := runSearch(context.Background(), s, se, &fakeLLM{}, schedule.Always); err != nil || se.calls != 0 || s.writes() != 0 {
		t.Errorf("err %v, calls %d, writes %d", err, se.calls, s.writes())
	}
}

func TestSearchRejected(t *testing.T) {
	s := &fakeStore{item: testItem()}

	if err := runSearch(context.Background(), s, &fakeSearcher{err: errSearchRejected}, &fakeLLM{}, schedule.Always); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(s.rejected, []string{"q"}) || len(s.saved) != 0 || len(s.kept) != 0 {
		t.Errorf("rejected %v, saved %d, kept %d", s.rejected, len(s.saved), len(s.kept))
	}
}

func TestSearchUnavailable(t *testing.T) {
	for _, err := range []error{port.ErrUnavailable, errors.New("brave status 401")} {
		s := &fakeStore{item: testItem()}

		if got := runSearch(context.Background(), s, &fakeSearcher{err: err}, &fakeLLM{}, schedule.Always); got == nil || s.writes() != 0 {
			t.Errorf("%v: err %v, writes %d", err, got, s.writes())
		}
	}
}

func TestSearchUnreadable(t *testing.T) {
	s := &fakeStore{item: testItem()}

	if err := runSearch(context.Background(), s, &fakeSearcher{err: errSearchUnreadable}, &fakeLLM{}, schedule.Always); err == nil || s.writes() != 0 {
		t.Errorf("err %v, writes %d", err, s.writes())
	}
}

func TestSearchFiltered(t *testing.T) {
	s := &fakeStore{item: testItem()}
	llm := &fakeLLM{answer: filterAnswer{Keep: []int{2, 0}}}

	if err := runSearch(context.Background(), s, &fakeSearcher{results: searchResults}, llm, schedule.Always); err != nil {
		t.Fatal(err)
	}

	if len(s.saved) != 1 || len(s.saved[0]) != 3 {
		t.Fatalf("saved %v", s.saved)
	}

	if len(s.kept) != 1 || !slices.Equal(s.kept[0], []int{2, 0}) || !slices.Equal(s.urls[0], []string{"https://c.example/3", "https://a.example/1"}) {
		t.Errorf("kept %v, urls %v", s.kept, s.urls)
	}

	if !strings.Contains(llm.req.Prompt, "Entity: volvo cars") {
		t.Errorf("prompt %q", llm.req.Prompt)
	}
}

func TestSearchClosedSchedule(t *testing.T) {
	s := &fakeStore{item: testItem()}
	llm := &fakeLLM{answer: filterAnswer{Keep: []int{0}}}

	if err := runSearch(context.Background(), s, &fakeSearcher{results: searchResults}, llm, closed); err != nil {
		t.Fatal(err)
	}

	if len(s.saved) != 1 || len(s.kept) != 0 || llm.req.System != "" {
		t.Errorf("saved %d, kept %d, llm called %v", len(s.saved), len(s.kept), llm.req.System != "")
	}
}

func TestSearchFilterError(t *testing.T) {
	s := &fakeStore{item: testItem()}

	if err := runSearch(context.Background(), s, &fakeSearcher{results: searchResults}, &fakeLLM{err: port.ErrUnavailable}, schedule.Always); err == nil {
		t.Error("no error")
	}

	if len(s.saved) != 1 || len(s.kept) != 0 {
		t.Errorf("saved %d, kept %d", len(s.saved), len(s.kept))
	}
}

func TestSearchNoResults(t *testing.T) {
	s := &fakeStore{item: testItem()}
	llm := &fakeLLM{}

	if err := runSearch(context.Background(), s, &fakeSearcher{}, llm, schedule.Always); err != nil {
		t.Fatal(err)
	}

	if len(s.saved) != 1 || len(s.saved[0]) != 0 || len(s.kept) != 1 || s.kept[0] == nil || len(s.kept[0]) != 0 || llm.req.System != "" {
		t.Errorf("saved %v, kept %v, llm called %v", s.saved, s.kept, llm.req.System != "")
	}
}

func TestSearchSavesOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &fakeStore{item: testItem()}

	runSearch(ctx, s, &fakeSearcher{results: searchResults, cancel: cancel}, &fakeLLM{answer: filterAnswer{}}, schedule.Always)

	if len(s.saved) != 1 || s.savedCtx != nil {
		t.Errorf("saved %d, ctx err %v", len(s.saved), s.savedCtx)
	}
}

func TestEnqueueFull(t *testing.T) {
	ids := make(chan string, 1)
	l := slog.New(slog.DiscardHandler)

	if !Enqueue(ids, l, "a") || Enqueue(ids, l, "b") {
		t.Error("want true then false")
	}
}

func TestStoreSearch(t *testing.T) {
	db := discoverDB(t)
	early := seedPost(t, db, seed{Trusted: true, Content: volvoArticle, Lang: "sv", Age: time.Hour})
	ids := discover(t, db, early, volvoTakeover)
	late := seedPost(t, db, seed{Trusted: true, Content: volvoArticle, Lang: "en"})
	discover(t, db, late, volvoTakeover)

	ctx := context.Background()
	s := NewStore(db)

	item, err := s.GetQuery(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}

	if item.Lang != "sv" || item.FromPostID != early.ID || item.BaseURL != "https://own.example" || item.Entity != "volvo_cars" || item.SearchedAt != nil {
		t.Errorf("item %+v", item)
	}

	if err := s.SaveResults(ctx, item.ID, searchResults); err != nil {
		t.Fatal(err)
	}

	if n, err := s.SearchesLastDay(ctx); err != nil || n != 1 {
		t.Errorf("searches %d, %v", n, err)
	}

	if err := s.SaveKept(ctx, item, []int{0}, []string{"https://a.example/1"}); err != nil {
		t.Fatal(err)
	}

	if n, err := s.PendingResults(ctx, dedupeWindow); err != nil || n != 1 {
		t.Errorf("pending %d, %v", n, err)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE id = $1 AND result->'kept' = '[0]' AND result->'results'->0->>'url' = 'https://a.example/1'`, item.ID); n != 1 {
		t.Errorf("stored result rows %d", n)
	}

	var fresh string
	if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) (err error) {
		fresh, _, err = insertQuery(ctx, tx, "saab", "acquisition", "Saab köper Kockums")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.MarkRejected(ctx, fresh); err != nil {
		t.Fatal(err)
	}

	if n := count(t, db, `SELECT count(*) FROM discovery_search_queries WHERE id = $1 AND searched_at IS NOT NULL AND result IS NULL`, fresh); n != 1 {
		t.Errorf("rejected rows %d", n)
	}

	if n, err := s.SearchesLastDay(ctx); err != nil || n != 1 {
		t.Errorf("searches after reject %d, %v", n, err)
	}
}

func TestSearchHeldBackLogged(t *testing.T) {
	cases := map[string]*fakeStore{
		"pending results": {item: testItem(), pending: 200},
		"daily budget":    {item: testItem(), used: 322},
	}

	for reason, s := range cases {
		var buf bytes.Buffer
		l := slog.New(slog.NewTextHandler(&buf, nil))

		if err := searchQuery(context.Background(), s, "q", &fakeSearcher{}, NewResultFilter(&fakeLLM{}), schedule.Always, l, l); err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(buf.String(), "search held back") || !strings.Contains(buf.String(), reason) {
			t.Errorf("%s: log %q", reason, buf.String())
		}
	}
}
