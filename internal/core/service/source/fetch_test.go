package source

import (
	"bytes"
	"context"
	"errors"
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
	feeds map[string][]string
}

func (f fakeFetcher) FetchFeed(_ context.Context, l *slog.Logger, url string) (<-chan string, error) {
	l.Info("fetch feed")

	items, ok := f.feeds[url]
	if !ok {
		return nil, errors.New("feed down")
	}

	out := make(chan string, len(items))
	for _, i := range items {
		out <- i
	}
	close(out)

	return out, nil
}

func (f fakeFetcher) Filter(_ context.Context, data string) (bool, error) {
	return !strings.HasSuffix(data, "/seen"), nil
}

func (f fakeFetcher) FetchPost(_ context.Context, l *slog.Logger, data string) (string, error) {
	l.Info("fetch post")

	if strings.HasSuffix(data, "/broken") {
		return "", errors.New("post down")
	}

	return data, nil
}

func (f fakeFetcher) ProcessFeed(_ context.Context, data string) (domain.RawPost, error) {
	p := domain.RawPost{URL: data}
	if strings.HasSuffix(data, "/en") {
		p.Lang = "en-GB"
	}

	return p, nil
}

func TestFetcherPlugin(t *testing.T) {
	var calls bytes.Buffer

	f := NewFetcher(fakeFetcher{feeds: map[string][]string{
		"https://a.example/feed": {"https://a.example/1", "https://a.example/seen", "https://a.example/broken"},
		"https://b.example/feed": {"https://b.example/1"},
	}}, slog.New(slog.NewTextHandler(&calls, nil)))

	in := make(chan domain.Source, 3)
	in <- domain.Source{ID: "a", URL: "https://a.example/feed"}
	in <- domain.Source{ID: "down", URL: "https://down.example/feed"}
	in <- domain.Source{ID: "b", URL: "https://b.example/feed"}
	close(in)

	ctx := context.Background()

	var got []string
	for p := range f.Plugin(ctx).Stream(ctx, slog.New(slog.DiscardHandler), in) {
		got = append(got, p.SourceID+" "+p.URL)
	}

	slices.Sort(got)

	if want := []string{"a https://a.example/1", "b https://b.example/1"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	for _, s := range []string{`msg="fetch feed" source_id=a`, `msg="fetch post" source_id=b`} {
		if !strings.Contains(calls.String(), s) {
			t.Errorf("calls log misses %s: %s", s, calls.String())
		}
	}
}

func TestFetcherSourceLang(t *testing.T) {
	f := NewFetcher(fakeFetcher{feeds: map[string][]string{
		"https://a.example/feed": {"https://a.example/1", "https://a.example/en"},
	}}, slog.New(slog.DiscardHandler))

	in := make(chan domain.Source, 1)
	in <- domain.Source{ID: "a", URL: "https://a.example/feed", Lang: "sv"}
	close(in)

	ctx := context.Background()

	var got []string
	for p := range f.Plugin(ctx).Stream(ctx, slog.New(slog.DiscardHandler), in) {
		got = append(got, p.URL+" "+p.Lang)
	}

	slices.Sort(got)

	if want := []string{"https://a.example/1 sv", "https://a.example/en en"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
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

type fakeBackfiller struct {
	fakeFetcher

	archive map[string][]string
	called  chan string
}

func (f fakeBackfiller) Backfill(_ context.Context, _ *slog.Logger, url string) (<-chan string, error) {
	if f.called != nil {
		f.called <- url
	}

	items, ok := f.archive[url]
	if !ok {
		return nil, port.ErrNoArchive
	}

	out := make(chan string, len(items))
	for _, i := range items {
		out <- i
	}
	close(out)

	return out, nil
}

func TestFetcherBackfill(t *testing.T) {
	ctx := context.Background()
	discard := slog.New(slog.DiscardHandler)

	if _, ok := NewFetcher(fakeFetcher{}, discard).Backfill(ctx); ok {
		t.Error("fetcher without a strategy can backfill")
	}

	f := NewFetcher(fakeBackfiller{
		fakeFetcher: fakeFetcher{feeds: map[string][]string{"https://a.example/feed": {"https://a.example/new"}}},
		archive:     map[string][]string{"https://a.example/feed": {"https://a.example/old", "https://a.example/seen"}},
	}, discard)

	plugin, ok := f.Backfill(ctx)
	if !ok {
		t.Fatal("backfiller cannot backfill")
	}

	in := make(chan domain.Source, 2)
	in <- domain.Source{ID: "a", URL: "https://a.example/feed"}
	in <- domain.Source{ID: "none", URL: "https://none.example/feed"}
	close(in)

	var got []string
	for p := range plugin.Stream(ctx, discard, in) {
		got = append(got, p.SourceID+" "+p.URL)
	}

	if want := []string{"a https://a.example/old"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestRunFeedsBackfillPlugin(t *testing.T) {
	s, _, _ := qualityServiceDB(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logged syncBuffer
	l := slog.New(slog.NewTextHandler(&logged, nil))

	called := make(chan string, 1)
	backfiller := fakeBackfiller{archive: map[string][]string{}, called: called}
	backfills := make(chan domain.Source, 2)

	go Run(ctx, s, map[string]Fetcher{"rss": NewFetcher(backfiller, slog.New(slog.DiscardHandler))}, backfills, l)

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
