package source

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
)

type fakeFetcher struct {
	feeds map[string][]domain.RawPost
}

func (f fakeFetcher) Fetch(_ context.Context, l *slog.Logger, url string) (<-chan domain.RawPost, error) {
	l.Info("fetch")

	return send(f.feeds, url)
}

type fakeBackfiller struct {
	fakeFetcher

	archive map[string][]domain.RawPost
	called  chan string
}

func (f fakeBackfiller) Backfill(_ context.Context, _ *slog.Logger, url string) (<-chan domain.RawPost, error) {
	if f.called != nil {
		f.called <- url
	}

	return send(f.archive, url)
}

func send(posts map[string][]domain.RawPost, url string) (<-chan domain.RawPost, error) {
	items, ok := posts[url]
	if !ok {
		return nil, port.ErrNoArchive
	}

	out := make(chan domain.RawPost, len(items))
	for _, p := range items {
		out <- p
	}
	close(out)

	return out, nil
}

// Run logs from several goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

func TestLane(t *testing.T) {
	var calls syncBuffer

	f := fakeFetcher{feeds: map[string][]domain.RawPost{
		"https://a.example/feed": {{URL: "https://a.example/1", Lang: "en-GB"}, {URL: "https://a.example/2"}},
		"https://b.example/feed": {{URL: "https://b.example/1"}},
	}}

	in := make(chan domain.Source, 3)
	in <- domain.Source{ID: "a", URL: "https://a.example/feed", Lang: "sv"}
	in <- domain.Source{ID: "none", URL: "https://none.example/feed"}
	in <- domain.Source{ID: "b", URL: "https://b.example/feed", Lang: "sv"}
	close(in)

	ctx := context.Background()

	var got []string
	for p := range lane(ctx, slog.New(slog.DiscardHandler), slog.New(slog.NewTextHandler(&calls, nil)), in, f.Fetch) {
		got = append(got, p.SourceID+" "+p.URL+" "+p.Lang)
	}

	slices.Sort(got)

	want := []string{"a https://a.example/1 en", "a https://a.example/2 sv", "b https://b.example/1 sv"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	if !strings.Contains(calls.String(), "source_id=a") {
		t.Errorf("calls log misses the source: %s", calls.String())
	}
}

func TestRunFeedsBackfillPlugin(t *testing.T) {
	s, _, _ := qualityServiceDB(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logged syncBuffer
	l := slog.New(slog.NewTextHandler(&logged, nil))
	discard := slog.New(slog.DiscardHandler)

	called := make(chan string, 1)
	backfills := make(chan domain.Source, 2)

	go Run(ctx, s, map[string]port.Fetcher{"rss": fakeBackfiller{called: called}}, backfills, l, discard)

	backfills <- domain.Source{ID: "x", Adapter: "rss", URL: "https://a.example/feed"}
	backfills <- domain.Source{ID: "y", Adapter: "page", URL: "https://b.example/"}

	select {
	case url := <-called:
		if url != "https://a.example/feed" {
			t.Errorf("backfilled %s", url)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rss backfill not called")
	}

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logged.String(), "no backfill strategy") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if out := logged.String(); !strings.Contains(out, "no backfill strategy") || !strings.Contains(out, "adapter=page") {
		t.Errorf("log %s", out)
	}
}

func TestRouteStopsOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan domain.Source)
	ins := map[string]chan domain.Source{"rss": in}

	done := make(chan struct{})
	go func() {
		route(ctx, make(chan domain.Source), ins, slog.New(slog.DiscardHandler), "no backfill strategy")
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("route kept running after ctx ended on an open channel")
	}

	if _, ok := <-in; ok {
		t.Error("input left open")
	}
}

