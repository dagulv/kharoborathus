package source

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer/html"
)

const (
	trimFormat      = "html"
	linksFormat     = "links"
	ruleSamples     = 8
	listingSamples  = 3
	listingGap      = 30 * time.Minute
	retryGeneration = 24 * time.Hour
	fetchTimeout    = 20 * time.Second
)

// Discovered domains are often seen once and never reach ruleSamples, so their pages must not pile up.
const maxSampleDomains = 200

type Trim struct {
	domainStore *domain.Store
	html        trimmer.Trimmer[html.Rule]
	linker      trimmer.Linker[html.LinkRule]
	llm         adapter.LLM
	client      *http.Client
	l           *slog.Logger

	generating chan struct{}

	mu            sync.Mutex
	samples       map[string][]trimmer.Sample
	sampledAt     map[string]uint64
	sampleSeq     uint64
	failedAt      map[string]time.Time
	listings      map[string][]trimmer.Sample
	listedAt      map[string]time.Time
	linksFailedAt map[string]time.Time
	triedOne      map[string]bool
}

func NewTrim(domains *domain.Store, llm adapter.LLM, l *slog.Logger) *Trim {
	return &Trim{
		domainStore:   domains,
		html:          html.New(llm, ""),
		linker:        html.NewLinker(llm, ""),
		llm:           llm,
		client:        safehttp.NewClient(fetchTimeout),
		l:             l,
		generating:    make(chan struct{}, 1),
		samples:       make(map[string][]trimmer.Sample),
		sampledAt:     make(map[string]uint64),
		failedAt:      make(map[string]time.Time),
		listings:      make(map[string][]trimmer.Sample),
		listedAt:      make(map[string]time.Time),
		linksFailedAt: make(map[string]time.Time),
		triedOne:      make(map[string]bool),
	}
}

func (t *Trim) Apply(ctx context.Context, l *slog.Logger, d domain.Domain, f feed.Feed, page []byte) feed.Feed {
	rule, ok := ruleOf[html.Rule](d, trimFormat)

	if e, err := t.html.Trim(rule, page); err != nil {
		t.l.Error("trim", "domain", d.BaseURL, "url", f.URL, "err", err)
	} else if e.OK() {
		f = withExtraction(f, e)
	} else {
		t.l.Debug("trim failed checks", "domain", d.BaseURL, "url", f.URL, "failures", e.Failures)
	}

	if !ok {
		t.collect(ctx, l.With("domain_id", d.ID), d, trimmer.Sample{URL: f.URL, Data: page})
	}

	return f
}

func (t *Trim) collect(ctx context.Context, l *slog.Logger, d domain.Domain, sample trimmer.Sample) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if time.Since(t.failedAt[d.ID]) < retryGeneration {
		return
	}

	samples := t.samples[d.ID]

	for _, s := range samples {
		if s.URL == sample.URL {
			return
		}
	}

	if len(samples) < ruleSamples {
		if samples == nil && len(t.samples) >= maxSampleDomains {
			t.evictOldestSamples()
		}

		samples = append(samples, sample)
		t.samples[d.ID] = samples
		t.sampleSeq++
		t.sampledAt[d.ID] = t.sampleSeq
	}

	if len(samples) < ruleSamples || !t.llm.Schedule().Active(time.Now()) {
		return
	}

	select {
	case t.generating <- struct{}{}:
	default:
		return
	}

	delete(t.samples, d.ID)
	delete(t.sampledAt, d.ID)

	go func() {
		defer func() { <-t.generating }()

		t.generate(ctx, l, d, samples)
	}()
}

func (t *Trim) evictOldestSamples() {
	oldest, first := "", true

	for id := range t.samples {
		if first || t.sampledAt[id] < t.sampledAt[oldest] {
			oldest, first = id, false
		}
	}

	delete(t.samples, oldest)
	delete(t.sampledAt, oldest)
}

func (t *Trim) generate(ctx context.Context, l *slog.Logger, d domain.Domain, samples []trimmer.Sample) {
	rule, err := t.html.GenerateRuleset(ctx, l, samples)

	switch {
	case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
		t.mu.Lock()
		t.samples[d.ID] = samples
		t.mu.Unlock()

		return
	case err != nil:
		t.mu.Lock()
		t.failedAt[d.ID] = time.Now()
		t.mu.Unlock()

		t.l.Warn("generate trim rule", "domain", d.BaseURL, "err", err)

		return
	}

	if err := t.saveRule(ctx, d.ID, trimFormat, rule); err != nil {
		t.l.Error("save trim rule", "domain", d.BaseURL, "err", err)
		return
	}

	t.l.Info("trim rule generated", "domain", d.BaseURL, "reasoning", rule.Reasoning)
}

func (t *Trim) saveRule(ctx context.Context, domainID, format string, rule any) error {
	raw, err := json.Marshal(rule)
	if err != nil {
		return err
	}

	d, err := t.domainStore.GetDomain(ctx, domainID)
	if err != nil {
		return err
	}

	if d.Metadata.Trim == nil {
		d.Metadata.Trim = make(map[string]json.RawMessage)
	}

	d.Metadata.Trim[format] = raw

	_, err = t.domainStore.UpdateMetadata(ctx, d.ID, d.Metadata)

	return err
}

func ruleOf[R any](d domain.Domain, format string) (rule R, ok bool) {
	raw, ok := d.Metadata.Trim[format]
	if !ok {
		return rule, false
	}

	if err := json.Unmarshal(raw, &rule); err != nil {
		var zero R
		return zero, false
	}

	return rule, true
}

func withExtraction(f feed.Feed, e trimmer.Extraction) feed.Feed {
	f.Content = e.Content

	if f.Title == "" {
		f.Title = e.Title
	}

	if len(f.Authors) == 0 && e.Author != "" {
		f.Authors = []string{e.Author}
	}

	if f.PostedAt.IsZero() {
		f.PostedAt = e.Published
	}

	return f
}
