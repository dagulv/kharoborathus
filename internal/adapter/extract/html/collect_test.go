package html

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

type idleLLM struct {
	mu    sync.Mutex
	calls int
}

func (*idleLLM) Schedule() schedule.Schedule { return schedule.Always }

func (*idleLLM) Prompt(context.Context, *slog.Logger, port.Request) (string, error) {
	return "", port.ErrUnavailable
}

func (f *idleLLM) Unmarshal(context.Context, *slog.Logger, port.Request, any) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	return port.ErrUnavailable
}

type fakeStore struct {
	mu    sync.Mutex
	rules map[string]json.RawMessage
}

func (f *fakeStore) SetExtractRule(_ context.Context, _, key string, rule json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.rules == nil {
		f.rules = make(map[string]json.RawMessage)
	}

	f.rules[key] = rule

	return nil
}

var discard = slog.New(slog.DiscardHandler)

// Takes and releases the generation slot, so a started generation has finished.
func wait(t *htmlExtractor) {
	t.generating <- struct{}{}
	<-t.generating
}

func TestCollectOncePerPage(t *testing.T) {
	tr := newExtractor(&idleLLM{}, nil, discard)
	d := domain.Domain{ID: "d"}
	s := site{body: "article-body"}

	for _, f := range articleFields {
		tr.collect(context.Background(), discard, d, f, pages(s, 0, 1)[0])
	}

	if len(tr.samples[d.ID]) != 1 || len(tr.requested[d.ID]) != len(articleFields) {
		t.Errorf("%d samples, %d fields requested", len(tr.samples[d.ID]), len(tr.requested[d.ID]))
	}
}

func TestCollectOnlyRequested(t *testing.T) {
	store := &fakeStore{}
	tr := newExtractor(&fakeLLM{answers: []any{goodRule()}}, store, discard)
	d := domain.Domain{ID: "d"}

	for _, p := range pages(site{body: "article-body", updated: "x"}, 0, ruleSamples) {
		tr.collect(context.Background(), discard, d, fieldContent, p)
	}

	wait(tr)

	if len(store.rules) != 1 || store.rules[string(fieldContent)] == nil {
		t.Errorf("saved %v, want only the content rule", store.rules)
	}
}

func TestCollectSkipsFailedField(t *testing.T) {
	tr := newExtractor(&idleLLM{}, nil, discard)
	d := domain.Domain{ID: "d"}
	tr.fail(fieldAuthors, d.ID)

	tr.collect(context.Background(), discard, d, fieldAuthors, pages(site{body: "article-body"}, 0, 1)[0])

	if len(tr.samples[d.ID]) != 0 {
		t.Errorf("%d samples for a failed field", len(tr.samples[d.ID]))
	}
}

func TestGenerateUnavailableKeepsSamples(t *testing.T) {
	tr := newExtractor(&idleLLM{}, nil, discard)
	d := domain.Domain{ID: "d"}
	samples := pages(site{body: "article-body"}, 0, ruleSamples)

	tr.generate(context.Background(), discard, d, []field{fieldContent, fieldAuthors}, samples)

	if len(tr.samples[d.ID]) != ruleSamples || !tr.requested[d.ID][fieldContent] || !tr.requested[d.ID][fieldAuthors] {
		t.Errorf("%d samples, requested %v", len(tr.samples[d.ID]), tr.requested[d.ID])
	}
}

func TestCollectEvictsOldestDomain(t *testing.T) {
	tr := newExtractor(&idleLLM{}, nil, discard)

	for i := range maxSampleDomains + 1 {
		tr.collect(context.Background(), discard, domain.Domain{ID: strconv.Itoa(i)}, fieldContent, sample{URL: fmt.Sprintf("https://d%d.example/a", i)})
	}

	// A new sample for domain 1 makes domain 2 the oldest.
	tr.collect(context.Background(), discard, domain.Domain{ID: "1"}, fieldContent, sample{URL: "https://d1.example/b"})
	tr.collect(context.Background(), discard, domain.Domain{ID: "new"}, fieldContent, sample{URL: "https://new.example/a"})

	if len(tr.samples) != maxSampleDomains {
		t.Errorf("%d domains sampled", len(tr.samples))
	}

	for id, want := range map[string]bool{"0": false, "1": true, "2": false, "3": true, "new": true} {
		if _, ok := tr.samples[id]; ok != want {
			t.Errorf("domain %s kept %v", id, ok)
		}

		if _, ok := tr.requested[id]; ok != want {
			t.Errorf("domain %s requests kept %v", id, ok)
		}
	}
}

func TestClientRefusesPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("private address fetched")
	}))
	defer srv.Close()

	if _, err := newExtractor(&idleLLM{}, nil, discard).client.Get(srv.URL); !errors.Is(err, safehttp.ErrPrivateAddress) {
		t.Errorf("err %v", err)
	}
}

func TestLinksTriesOneListingFirst(t *testing.T) {
	llm := &fakeLLM{answers: []any{LinkRule{Reasoning: "no list"}}}
	tr := newExtractor(llm, nil, discard)
	d := domain.Domain{ID: "d"}
	s := listings(1)[0]

	if links := tr.withoutLinkRule(context.Background(), discard, d, s); links != nil {
		t.Errorf("links %v", links)
	}

	if llm.calls != len(temperatures) || len(tr.listings[d.ID]) != 1 {
		t.Errorf("%d llm calls, %d listings kept", llm.calls, len(tr.listings[d.ID]))
	}

	// A failed single listing falls back to collecting, so it is not tried again.
	tr.listedAt[d.ID] = time.Time{}
	tr.withoutLinkRule(context.Background(), discard, d, s)

	if llm.calls != len(temperatures) || len(tr.listings[d.ID]) != 2 {
		t.Errorf("%d llm calls, %d listings kept", llm.calls, len(tr.listings[d.ID]))
	}
}

func TestLinksRetriesOneListingWhenUnavailable(t *testing.T) {
	llm := &idleLLM{}
	tr := newExtractor(llm, nil, discard)
	d := domain.Domain{ID: "d"}

	tr.withoutLinkRule(context.Background(), discard, d, listings(1)[0])
	tr.withoutLinkRule(context.Background(), discard, d, listings(1)[0])

	if llm.calls != 2 {
		t.Errorf("%d llm calls", llm.calls)
	}
}
