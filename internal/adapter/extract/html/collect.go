package html

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/fetcher"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/logs"
)

const (
	ruleSamples     = 8
	listingSamples  = 3
	listingGap      = 30 * time.Minute
	retryGeneration = 24 * time.Hour
)

// Discovered domains are often seen once and never reach ruleSamples, so their pages must not pile up.
const maxSampleDomains = 200

var articleFields = []field{fieldContent, fieldTitle, fieldAuthors, fieldPostedAt}

// Only requested fields get rules, so a feed that carries its own authors never pays for an authors rule.
func (t *htmlExtractor) collect(ctx context.Context, l *slog.Logger, d domain.Domain, f field, s sample) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.failing(f, d.ID) {
		return
	}

	if t.requested[d.ID] == nil {
		t.requested[d.ID] = make(map[field]bool)
	}

	t.requested[d.ID][f] = true

	samples := t.samples[d.ID]

	for _, have := range samples {
		if have.URL == s.URL {
			return
		}
	}

	if len(samples) < ruleSamples {
		if samples == nil && len(t.samples) >= maxSampleDomains {
			t.evictOldestSamples()
		}

		samples = append(samples, s)
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

	var fields []field

	for _, af := range articleFields {
		if t.requested[d.ID][af] && !t.failing(af, d.ID) {
			fields = append(fields, af)
		}
	}

	delete(t.samples, d.ID)
	delete(t.sampledAt, d.ID)
	delete(t.requested, d.ID)

	go func() {
		defer func() { <-t.generating }()

		t.generate(ctx, l, d, fields, samples)
	}()
}

// Callers hold t.mu.
func (t *htmlExtractor) failing(f field, domainID string) bool {
	return time.Since(t.failedAt[f][domainID]) < retryGeneration
}

// Callers hold t.mu.
func (t *htmlExtractor) fail(f field, domainID string) {
	if t.failedAt[f] == nil {
		t.failedAt[f] = make(map[string]time.Time)
	}

	t.failedAt[f][domainID] = time.Now()
}

func (t *htmlExtractor) evictOldestSamples() {
	oldest, first := "", true

	for id := range t.samples {
		if first || t.sampledAt[id] < t.sampledAt[oldest] {
			oldest, first = id, false
		}
	}

	delete(t.samples, oldest)
	delete(t.sampledAt, oldest)
	delete(t.requested, oldest)
}

func (t *htmlExtractor) generate(ctx context.Context, l *slog.Logger, d domain.Domain, fields []field, samples []sample) {
	for i, f := range fields {
		rule, reasoning, err := t.generateRule(ctx, l, f, samples)

		switch {
		case errors.Is(err, port.ErrUnavailable) || ctx.Err() != nil:
			t.mu.Lock()
			t.samples[d.ID] = samples
			t.requested[d.ID] = make(map[field]bool)

			for _, rest := range fields[i:] {
				t.requested[d.ID][rest] = true
			}

			t.mu.Unlock()

			return
		case err != nil:
			t.mu.Lock()
			t.fail(f, d.ID)
			t.mu.Unlock()

			t.l.Warn("generate extract rule", "domain", d.BaseURL, "field", f, "err", err)

			continue
		}

		if err := t.saveRule(ctx, d.ID, f, rule); err != nil {
			t.l.Error("save extract rule", "domain", d.BaseURL, "field", f, "err", err)
			continue
		}

		t.l.Info("extract rule generated", "domain", d.BaseURL, "field", f, "reasoning", reasoning)
	}
}

func (t *htmlExtractor) generateRule(ctx context.Context, l *slog.Logger, f field, samples []sample) (rule any, reasoning string, err error) {
	if f == fieldContent {
		r, err := t.generateContent(ctx, l, samples)
		return r, r.Reasoning, err
	}

	r, err := t.generateField(ctx, l, f, samples)

	return r, r.Reasoning, err
}

func (t *htmlExtractor) Links(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte) ([]string, error) {
	s := sample{URL: url, Data: listing}

	rule, ok := ruleOf[LinkRule](d, fieldLinks)
	if !ok {
		return t.withoutLinkRule(ctx, l.With("domain_id", d.ID), d, s), nil
	}

	links, err := applyLinks(rule, s)
	if err != nil {
		return nil, fmt.Errorf("err links: %w", err)
	}

	return links, nil
}

func (t *htmlExtractor) withoutLinkRule(ctx context.Context, l *slog.Logger, d domain.Domain, s sample) []string {
	if links, ok := t.linksFromOneListing(ctx, l, d, s); ok {
		return links
	}

	t.collectListing(ctx, l, d, s)

	return nil
}

// A new source gets its posts on the first fetch. One listing cannot show which headlines change,
// so a failure falls back to collecting listingSamples and is not tried again until a restart.
func (t *htmlExtractor) linksFromOneListing(ctx context.Context, l *slog.Logger, d domain.Domain, s sample) ([]string, bool) {
	t.mu.Lock()
	skip := t.triedOne[d.ID] || t.failing(fieldLinks, d.ID)
	t.mu.Unlock()

	if skip || !t.llm.Schedule().Active(time.Now()) {
		return nil, false
	}

	select {
	case t.generating <- struct{}{}:
	default:
		return nil, false
	}

	defer func() { <-t.generating }()

	rule, err := t.generateLinkRule(ctx, l, []sample{s}, t.fetcher(l))
	if errors.Is(err, port.ErrUnavailable) || ctx.Err() != nil {
		return nil, false
	}

	t.mu.Lock()
	t.triedOne[d.ID] = true
	t.mu.Unlock()

	if err != nil {
		t.l.Info("link rule from one listing failed, collecting more", "domain", d.BaseURL, "err", err)
		return nil, false
	}

	if err := t.saveRule(ctx, d.ID, fieldLinks, rule); err != nil {
		t.l.Error("save link rule", "domain", d.BaseURL, "err", err)
	} else {
		t.l.Info("link rule generated from one listing", "domain", d.BaseURL, "reasoning", rule.Reasoning)
	}

	links, err := applyLinks(rule, s)
	if err != nil {
		t.l.Error("links", "domain", d.BaseURL, "err", err)
		return nil, false
	}

	return links, true
}

func (t *htmlExtractor) fetcher(l *slog.Logger) fetchFunc {
	client := logs.Client(t.client, l, "trim")

	return func(ctx context.Context, url string) ([]byte, error) {
		return fetcher.FetchHTML(ctx, client, url)
	}
}

// Snapshots are spaced by listingGap so headlines change between them and show up as unique.
func (t *htmlExtractor) collectListing(ctx context.Context, l *slog.Logger, d domain.Domain, s sample) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.failing(fieldLinks, d.ID) {
		return
	}

	samples := t.listings[d.ID]

	if len(samples) < listingSamples && time.Since(t.listedAt[d.ID]) >= listingGap {
		samples = append(samples, s)
		t.listings[d.ID] = samples
		t.listedAt[d.ID] = time.Now()
	}

	if len(samples) < listingSamples || !t.llm.Schedule().Active(time.Now()) {
		return
	}

	select {
	case t.generating <- struct{}{}:
	default:
		return
	}

	delete(t.listings, d.ID)

	go func() {
		defer func() { <-t.generating }()

		t.generateLinks(ctx, l, d, samples)
	}()
}

func (t *htmlExtractor) generateLinks(ctx context.Context, l *slog.Logger, d domain.Domain, samples []sample) {
	rule, err := t.generateLinkRule(ctx, l, samples, t.fetcher(l))

	switch {
	case errors.Is(err, port.ErrUnavailable) || ctx.Err() != nil:
		t.mu.Lock()
		t.listings[d.ID] = samples
		t.mu.Unlock()

		return
	case err != nil:
		t.mu.Lock()
		t.fail(fieldLinks, d.ID)
		t.mu.Unlock()

		t.l.Warn("generate link rule", "domain", d.BaseURL, "err", err)

		return
	}

	if err := t.saveRule(ctx, d.ID, fieldLinks, rule); err != nil {
		t.l.Error("save link rule", "domain", d.BaseURL, "err", err)
		return
	}

	t.l.Info("link rule generated", "domain", d.BaseURL, "reasoning", rule.Reasoning)
}
