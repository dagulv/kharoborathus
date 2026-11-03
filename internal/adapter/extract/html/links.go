package html

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/andybalholm/cascadia"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/invopop/jsonschema"
)

type fetchFunc func(ctx context.Context, url string) ([]byte, error)

const (
	minListings     = 1
	minLinks        = 5
	linkChecks      = 4
	minArticleShare = 0.75
)

type LinkRule struct {
	Links     *string `json:"links" jsonschema:"nullable"`
	Reasoning string  `json:"reasoning"`
}

func (LinkRule) JSONSchemaExtend(s *jsonschema.Schema) {
	for name, desc := range prompts.linkFields {
		if p, ok := s.Properties.Get(name); ok {
			p.Description = desc
		}
	}
}

func (r LinkRule) Abstained() bool {
	return r.Links == nil || strings.TrimSpace(*r.Links) == ""
}

func (r LinkRule) sanitize() LinkRule {
	if r.Links != nil {
		v := strings.TrimSpace(*r.Links)
		r.Links = &v

		if v == "" || v == "null" {
			r.Links = nil
		}
	}

	r.Reasoning = truncate(collapse(r.Reasoning), 200)

	return r
}

func applyLinks(rule LinkRule, listing sample) ([]string, error) {
	if rule.Abstained() {
		return nil, ErrAbstained
	}

	if _, err := parseSelector(*rule.Links); err != nil {
		return nil, err
	}

	sel, err := cascadia.Parse(*rule.Links)
	if err != nil {
		return nil, fmt.Errorf("extract: compile %q: %w", *rule.Links, err)
	}

	base, err := url.Parse(listing.URL)
	if err != nil {
		return nil, fmt.Errorf("extract: parse url %s: %w", listing.URL, err)
	}

	doc, err := parse(listing.Data)
	if err != nil {
		return nil, fmt.Errorf("extract: parse %s: %w", listing.URL, err)
	}

	seen := map[string]bool{base.String(): true}

	var links []string

	for _, n := range cascadia.QueryAll(doc.body, sel) {
		u, err := base.Parse(strings.TrimSpace(attr(n, "href")))
		if err != nil || u.Host != base.Host || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}

		u.Fragment = ""
		link := u.String()

		if seen[link] {
			continue
		}

		seen[link] = true
		links = append(links, link)
	}

	return links, nil
}

func (t *htmlExtractor) generateLinkRule(ctx context.Context, l *slog.Logger, samples []sample, fetch fetchFunc) (LinkRule, error) {
	if len(samples) < minListings {
		return LinkRule{}, fmt.Errorf("extract: %d listings, at least %d needed", len(samples), minListings)
	}

	docs := make([]document, 0, len(samples))

	for _, p := range samples {
		doc, err := parse(p.Data)
		if err != nil {
			return LinkRule{}, fmt.Errorf("extract: parse %s: %w", p.URL, err)
		}

		docs = append(docs, doc)
	}

	skel := buildSkeleton(docs)

	req := port.Request{
		System: prompts.linksSystem(len(samples)),
		Prompt: prompts.user(skel),
		Model:  t.model,
	}

	errs := []error{ErrNoRule}

	for _, temp := range temperatures {
		req.Temperature = &temp

		var rule LinkRule

		if err := t.llm.Unmarshal(ctx, l, req, &rule); err != nil {
			if errors.Is(err, port.ErrUnavailable) || ctx.Err() != nil {
				return LinkRule{}, err
			}

			errs = append(errs, err)
			continue
		}

		rule = rule.sanitize()

		err := checkLinkRule(ctx, rule, skel, samples, fetch)
		if ctx.Err() != nil {
			return LinkRule{}, ctx.Err()
		}

		if err != nil {
			errs = append(errs, err)
			continue
		}

		return rule, nil
	}

	return LinkRule{}, errors.Join(errs...)
}

func checkLinkRule(ctx context.Context, rule LinkRule, skel Skeleton, samples []sample, fetch fetchFunc) error {
	if rule.Abstained() {
		return ErrAbstained
	}

	if err := skel.vocab.ground(*rule.Links); err != nil {
		return err
	}

	var links []string

	for _, s := range samples {
		l, err := applyLinks(rule, s)
		if err != nil {
			return err
		}

		if len(l) < minLinks {
			return fmt.Errorf("extract: %s: %d links, at least %d needed", s.URL, len(l), minLinks)
		}

		links = l
	}

	return checkArticles(ctx, links, fetch)
}

// Junk selectors match navigation or sections, so most linked pages fail generic extraction.
func checkArticles(ctx context.Context, links []string, fetch fetchFunc) error {
	n := min(linkChecks, len(links))
	articles := 0

	for i := range n {
		page, err := fetch(ctx, links[i*len(links)/n])
		if err != nil {
			continue
		}

		if contentGeneric(page, FieldRule{}).OK() {
			articles++
		}
	}

	if float64(articles) < minArticleShare*float64(n) {
		return fmt.Errorf("extract: %d of %d linked pages are articles", articles, n)
	}

	return nil
}
