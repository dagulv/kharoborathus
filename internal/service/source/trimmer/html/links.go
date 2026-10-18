package html

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/andybalholm/cascadia"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
	"github.com/invopop/jsonschema"
)

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

var _ trimmer.Linker[LinkRule] = (*htmlLinker)(nil)

type htmlLinker struct {
	llm   adapter.LLM
	model string
}

func NewLinker(llm adapter.LLM, model string) trimmer.Linker[LinkRule] {
	return &htmlLinker{llm: llm, model: cmp.Or(model, adapter.ModelFlash)}
}

func (t *htmlLinker) Links(rule LinkRule, listing trimmer.Sample) ([]string, error) {
	if rule.Abstained() {
		return nil, ErrAbstained
	}

	if _, err := parseSelector(*rule.Links); err != nil {
		return nil, err
	}

	sel, err := cascadia.Parse(*rule.Links)
	if err != nil {
		return nil, fmt.Errorf("trimmer: compile %q: %w", *rule.Links, err)
	}

	base, err := url.Parse(listing.URL)
	if err != nil {
		return nil, fmt.Errorf("trimmer: parse url %s: %w", listing.URL, err)
	}

	doc, err := parse(listing.Data)
	if err != nil {
		return nil, fmt.Errorf("trimmer: parse %s: %w", listing.URL, err)
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

func (t *htmlLinker) GenerateRuleset(ctx context.Context, samples []trimmer.Sample, fetch trimmer.Fetch) (LinkRule, error) {
	if len(samples) < minListings {
		return LinkRule{}, fmt.Errorf("trimmer: %d listings, at least %d needed", len(samples), minListings)
	}

	docs := make([]document, 0, len(samples))

	for _, p := range samples {
		doc, err := parse(p.Data)
		if err != nil {
			return LinkRule{}, fmt.Errorf("trimmer: parse %s: %w", p.URL, err)
		}

		docs = append(docs, doc)
	}

	skel := buildSkeleton(docs)

	req := adapter.Request{
		System: prompts.linksSystem(len(samples)),
		Prompt: prompts.user(skel),
		Model:  t.model,
	}

	errs := []error{trimmer.ErrNoRule}

	for _, temp := range temperatures {
		req.Temperature = &temp

		var rule LinkRule

		if err := t.llm.Unmarshal(ctx, req, &rule); err != nil {
			if errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil {
				return LinkRule{}, err
			}

			errs = append(errs, err)
			continue
		}

		rule = rule.sanitize()

		err := t.check(ctx, rule, skel, samples, fetch)
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

func (t *htmlLinker) check(ctx context.Context, rule LinkRule, skel Skeleton, samples []trimmer.Sample, fetch trimmer.Fetch) error {
	if rule.Abstained() {
		return ErrAbstained
	}

	if err := skel.vocab.ground(*rule.Links); err != nil {
		return err
	}

	var links []string

	for _, s := range samples {
		l, err := t.Links(rule, s)
		if err != nil {
			return err
		}

		if len(l) < minLinks {
			return fmt.Errorf("trimmer: %s: %d links, at least %d needed", s.URL, len(l), minLinks)
		}

		links = l
	}

	return checkArticles(ctx, links, fetch)
}

// Junk selectors match navigation or sections, so most linked pages fail generic extraction.
func checkArticles(ctx context.Context, links []string, fetch trimmer.Fetch) error {
	n := min(linkChecks, len(links))
	articles := 0

	for i := range n {
		page, err := fetch(ctx, links[i*len(links)/n])
		if err != nil {
			continue
		}

		if trimGeneric(page).OK() {
			articles++
		}
	}

	if float64(articles) < minArticleShare*float64(n) {
		return fmt.Errorf("trimmer: %d of %d linked pages are articles", articles, n)
	}

	return nil
}
