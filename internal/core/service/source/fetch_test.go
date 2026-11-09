package source

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
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
