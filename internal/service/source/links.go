package source

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagulv/kharoborathus/internal/service/domain"
	robotadapter "github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer/html"
)

var _ adapter.Linker = (*Trim)(nil)

func (t *Trim) Links(ctx context.Context, url string, listing []byte) ([]string, error) {
	d, err := t.domainStore.GetOrCreateDomain(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("err get domain: %w", err)
	}

	sample := trimmer.Sample{URL: url, Data: listing}

	rule, ok := ruleOf[html.LinkRule](d, linksFormat)
	if !ok {
		return t.withoutRule(ctx, d, sample), nil
	}

	links, err := t.linker.Links(rule, sample)
	if err != nil {
		return nil, fmt.Errorf("err links: %w", err)
	}

	return links, nil
}

func (t *Trim) withoutRule(ctx context.Context, d domain.Domain, sample trimmer.Sample) []string {
	if links, ok := t.fromOneListing(ctx, d, sample); ok {
		return links
	}

	t.collectListing(ctx, d, sample)

	return nil
}

// A new source gets its posts on the first fetch. One listing cannot show which headlines change,
// so a failure falls back to collecting listingSamples and is not tried again until a restart.
func (t *Trim) fromOneListing(ctx context.Context, d domain.Domain, sample trimmer.Sample) ([]string, bool) {
	t.mu.Lock()
	skip := t.triedOne[d.ID] || time.Since(t.linksFailedAt[d.ID]) < retryGeneration
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

	rule, err := t.linker.GenerateRuleset(ctx, []trimmer.Sample{sample}, t.fetchHTML)
	if errors.Is(err, robotadapter.ErrUnavailable) || ctx.Err() != nil {
		return nil, false
	}

	t.mu.Lock()
	t.triedOne[d.ID] = true
	t.mu.Unlock()

	if err != nil {
		t.l.Info("link rule from one listing failed, collecting more", "domain", d.BaseURL, "err", err)
		return nil, false
	}

	if err := t.saveRule(ctx, d.ID, linksFormat, rule); err != nil {
		t.l.Error("save link rule", "domain", d.BaseURL, "err", err)
	} else {
		t.l.Info("link rule generated from one listing", "domain", d.BaseURL, "reasoning", rule.Reasoning)
	}

	links, err := t.linker.Links(rule, sample)
	if err != nil {
		t.l.Error("links", "domain", d.BaseURL, "err", err)
		return nil, false
	}

	return links, true
}

func (t *Trim) fetchHTML(ctx context.Context, url string) ([]byte, error) {
	return adapter.FetchHTML(ctx, t.client, url)
}

// Snapshots are spaced by listingGap so headlines change between them and show up as unique.
func (t *Trim) collectListing(ctx context.Context, d domain.Domain, sample trimmer.Sample) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if time.Since(t.linksFailedAt[d.ID]) < retryGeneration {
		return
	}

	samples := t.listings[d.ID]

	if len(samples) < listingSamples && time.Since(t.listedAt[d.ID]) >= listingGap {
		samples = append(samples, sample)
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

		t.generateLinks(ctx, d, samples)
	}()
}

func (t *Trim) generateLinks(ctx context.Context, d domain.Domain, samples []trimmer.Sample) {
	rule, err := t.linker.GenerateRuleset(ctx, samples, t.fetchHTML)

	switch {
	case errors.Is(err, robotadapter.ErrUnavailable) || ctx.Err() != nil:
		t.mu.Lock()
		t.listings[d.ID] = samples
		t.mu.Unlock()

		return
	case err != nil:
		t.mu.Lock()
		t.linksFailedAt[d.ID] = time.Now()
		t.mu.Unlock()

		t.l.Warn("generate link rule", "domain", d.BaseURL, "err", err)

		return
	}

	if err := t.saveRule(ctx, d.ID, linksFormat, rule); err != nil {
		t.l.Error("save link rule", "domain", d.BaseURL, "err", err)
		return
	}

	t.l.Info("link rule generated", "domain", d.BaseURL, "reasoning", rule.Reasoning)
}
