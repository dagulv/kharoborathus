package source

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

type fakeExtractor struct {
	contentErr error
	calls      []string
}

var extracted = time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)

func (f *fakeExtractor) Links(context.Context, *slog.Logger, domain.Domain, string, []byte) ([]string, error) {
	return nil, nil
}

func (f *fakeExtractor) Next(context.Context, *slog.Logger, domain.Domain, string, []byte, int) (string, error) {
	return "", nil
}

func (f *fakeExtractor) Content(context.Context, *slog.Logger, domain.Domain, string, []byte) (string, error) {
	f.calls = append(f.calls, "content")
	return "extracted", f.contentErr
}

func (f *fakeExtractor) Title(context.Context, *slog.Logger, domain.Domain, string, []byte) (string, error) {
	f.calls = append(f.calls, "title")
	return "extracted title", nil
}

func (f *fakeExtractor) Authors(context.Context, *slog.Logger, domain.Domain, string, []byte) ([]string, error) {
	f.calls = append(f.calls, "authors")
	return []string{"Extracted"}, nil
}

func (f *fakeExtractor) PostedAt(context.Context, *slog.Logger, domain.Domain, string, []byte) (time.Time, error) {
	f.calls = append(f.calls, "postedAt")
	return extracted, nil
}

func TestExtractKeepsFetched(t *testing.T) {
	discard := slog.New(slog.DiscardHandler)
	fetchedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	f := &fakeExtractor{}
	s := &Service{Extractor: f}

	p := s.extract(context.Background(), discard, discard, domain.Domain{}, domain.RawPost{
		RawContent: "<html></html>", Title: "fetched", Authors: []string{"Fetched"}, PostedAt: &fetchedAt,
	})

	if p.Title != "fetched" || !slices.Equal(p.Authors, []string{"Fetched"}) || !p.PostedAt.Equal(fetchedAt) || p.Content != "extracted" {
		t.Errorf("post %+v", p)
	}

	if !slices.Equal(f.calls, []string{"content"}) {
		t.Errorf("calls %v", f.calls)
	}

	p = s.extract(context.Background(), discard, discard, domain.Domain{}, domain.RawPost{RawContent: "<html></html>"})

	if p.Title != "extracted title" || !slices.Equal(p.Authors, []string{"Extracted"}) || !p.PostedAt.Equal(extracted) {
		t.Errorf("post %+v", p)
	}

	s.Extractor = &fakeExtractor{contentErr: errors.New("checks failed")}

	if p := s.extract(context.Background(), discard, discard, domain.Domain{}, domain.RawPost{RawContent: "<html></html>", Content: "fetched"}); p.Content != "fetched" {
		t.Errorf("failed content replaced fetched content: %q", p.Content)
	}
}

func TestExtractKeepsLongerContent(t *testing.T) {
	discard := slog.New(slog.DiscardHandler)
	s := &Service{Extractor: &fakeExtractor{}}

	cases := []struct{ feed, want string }{
		{"", "extracted"},
		{"<p>feed article that is longer</p>", "<p>feed article that is longer</p>"},
		{`<div class="a very long class list here"><b>x</b></div>`, "extracted"},
	}

	for _, c := range cases {
		p := s.extract(context.Background(), discard, discard, domain.Domain{}, domain.RawPost{RawContent: "<html></html>", Content: c.feed})
		if p.Content != c.want {
			t.Errorf("feed %q: content %q, want %q", c.feed, p.Content, c.want)
		}
	}
}

type emptyExtractor struct{ fakeExtractor }

func (*emptyExtractor) Content(context.Context, *slog.Logger, domain.Domain, string, []byte) (string, error) {
	return "", nil
}

func TestExtractMissing(t *testing.T) {
	s, base := qualityService(t, nil)
	s.Extractor = &emptyExtractor{}
	rec := newCapture()
	s.Calls = slog.New(rec)
	ctx := context.Background()
	l := slog.New(slog.DiscardHandler)

	p, err := s.claim(ctx, l, domain.RawPost{URL: "https://a.example/empty", SourceID: base.SourceID})
	if err != nil {
		t.Fatal(err)
	}

	p.RawContent = "<html></html>"

	if _, err := s.Extract(ctx, l, p); !errors.Is(err, pipeline.ErrSkip) {
		t.Fatalf("err %v", err)
	}

	got, err := s.RawPostStore.GetRawPost(ctx, p.ID)
	if err != nil || got.Status != domain.StatusMissing {
		t.Errorf("status %q, %v", got.Status, err)
	}

	if line, last := rec.stepLine(p.ID), rec.last(p.ID); line != "extract:stopped" || last.attrs["reason"] != "no content found" {
		t.Errorf("steps %q, reason %q", line, last.attrs["reason"])
	}
}
