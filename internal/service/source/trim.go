package source

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer/html"
)

const (
	trimFormat      = "html"
	ruleSamples     = 8
	retryGeneration = 24 * time.Hour
)

type Trim struct {
	domainStore *domain.Store
	html        trimmer.Trimmer[html.Rule]
	llm         adapter.LLM
	l           *slog.Logger

	generating chan struct{}

	mu       sync.Mutex
	samples  map[string][]trimmer.Sample
	failedAt map[string]time.Time
}

func NewTrim(domains *domain.Store, llm adapter.LLM, l *slog.Logger) *Trim {
	return &Trim{
		domainStore: domains,
		html:        html.New(llm, ""),
		llm:         llm,
		l:           l,
		generating:  make(chan struct{}, 1),
		samples:     make(map[string][]trimmer.Sample),
		failedAt:    make(map[string]time.Time),
	}
}

func (t *Trim) Apply(ctx context.Context, d domain.Domain, f feed.Feed, page []byte) feed.Feed {
	rule, ok := ruleOf(d)

	if e, err := t.html.Trim(rule, page); err != nil {
		t.l.Error("trim", "domain", d.BaseURL, "url", f.URL, "err", err)
	} else if e.OK() {
		f = withExtraction(f, e)
	} else {
		t.l.Debug("trim failed checks", "domain", d.BaseURL, "url", f.URL, "failures", e.Failures)
	}

	if !ok {
		t.collect(ctx, d, trimmer.Sample{URL: f.URL, Data: page})
	}

	return f
}

func (t *Trim) collect(ctx context.Context, d domain.Domain, sample trimmer.Sample) {
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
		samples = append(samples, sample)
		t.samples[d.ID] = samples
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

	go func() {
		defer func() { <-t.generating }()

		t.generate(ctx, d, samples)
	}()
}

func (t *Trim) generate(ctx context.Context, d domain.Domain, samples []trimmer.Sample) {
	rule, err := t.html.GenerateRuleset(ctx, samples)

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

	if err := t.saveRule(ctx, d.ID, rule); err != nil {
		t.l.Error("save trim rule", "domain", d.BaseURL, "err", err)
		return
	}

	t.l.Info("trim rule generated", "domain", d.BaseURL, "reasoning", rule.Reasoning)
}

func (t *Trim) saveRule(ctx context.Context, domainID string, rule html.Rule) error {
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

	d.Metadata.Trim[trimFormat] = raw

	_, err = t.domainStore.UpdateMetadata(ctx, d.ID, d.Metadata)

	return err
}

func ruleOf(d domain.Domain) (rule html.Rule, ok bool) {
	raw, ok := d.Metadata.Trim[trimFormat]
	if !ok {
		return rule, false
	}

	if err := json.Unmarshal(raw, &rule); err != nil {
		return html.Rule{}, false
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
