package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer/html"
)

type idleLLM struct{}

func (idleLLM) Schedule() schedule.Schedule { return schedule.Always }

func (idleLLM) Prompt(context.Context, adapter.Request) (string, error) {
	return "", adapter.ErrUnavailable
}

func (idleLLM) Unmarshal(context.Context, adapter.Request, any) error {
	return adapter.ErrUnavailable
}

func TestCollectEvictsOldestDomain(t *testing.T) {
	tr := NewTrim(nil, idleLLM{}, slog.New(slog.DiscardHandler))

	for i := range maxSampleDomains + 1 {
		tr.collect(context.Background(), domain.Domain{ID: strconv.Itoa(i)}, trimmer.Sample{URL: fmt.Sprintf("https://d%d.example/a", i)})
	}

	// A new sample for domain 1 makes domain 2 the oldest.
	tr.collect(context.Background(), domain.Domain{ID: "1"}, trimmer.Sample{URL: "https://d1.example/b"})
	tr.collect(context.Background(), domain.Domain{ID: "new"}, trimmer.Sample{URL: "https://new.example/a"})

	if len(tr.samples) != maxSampleDomains {
		t.Errorf("%d domains sampled", len(tr.samples))
	}

	for id, want := range map[string]bool{"0": false, "1": true, "2": false, "3": true, "new": true} {
		if _, ok := tr.samples[id]; ok != want {
			t.Errorf("domain %s kept %v", id, ok)
		}
	}
}

func TestTrimClientRefusesPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("private address fetched")
	}))
	defer srv.Close()

	if _, err := NewTrim(nil, idleLLM{}, slog.New(slog.DiscardHandler)).client.Get(srv.URL); !errors.Is(err, safehttp.ErrPrivateAddress) {
		t.Errorf("err %v", err)
	}
}

type failingLinker struct {
	err   error
	calls int
}

func (f *failingLinker) GenerateRuleset(context.Context, []trimmer.Sample, trimmer.Fetch) (html.LinkRule, error) {
	f.calls++
	return html.LinkRule{}, f.err
}

func (f *failingLinker) Links(html.LinkRule, trimmer.Sample) ([]string, error) {
	return nil, nil
}

func TestLinksTriesOneListingFirst(t *testing.T) {
	linker := &failingLinker{err: trimmer.ErrNoRule}
	tr := NewTrim(nil, idleLLM{}, slog.New(slog.DiscardHandler))
	tr.linker = linker

	d := domain.Domain{ID: "d"}
	sample := trimmer.Sample{URL: "https://d.example/"}

	if links := tr.withoutRule(context.Background(), d, sample); links != nil {
		t.Errorf("links %v", links)
	}

	if linker.calls != 1 || len(tr.listings[d.ID]) != 1 {
		t.Errorf("%d generations, %d listings kept", linker.calls, len(tr.listings[d.ID]))
	}

	// A failed single listing falls back to collecting, so it is not tried again.
	tr.listedAt[d.ID] = time.Time{}
	tr.withoutRule(context.Background(), d, sample)

	if linker.calls != 1 || len(tr.listings[d.ID]) != 2 {
		t.Errorf("%d generations, %d listings kept", linker.calls, len(tr.listings[d.ID]))
	}
}

func TestLinksRetriesOneListingWhenUnavailable(t *testing.T) {
	linker := &failingLinker{err: adapter.ErrUnavailable}
	tr := NewTrim(nil, idleLLM{}, slog.New(slog.DiscardHandler))
	tr.linker = linker

	d := domain.Domain{ID: "d"}

	tr.withoutRule(context.Background(), d, trimmer.Sample{URL: "https://d.example/"})
	tr.withoutRule(context.Background(), d, trimmer.Sample{URL: "https://d.example/"})

	if linker.calls != 2 {
		t.Errorf("%d generations", linker.calls)
	}
}
