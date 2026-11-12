package html

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/invopop/jsonschema"
	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	maxCandidates = 80
	candidateText = 40
	pageNumber    = "{n}"
)

// How often a wanted pages rule checks again for off-peak and a free generation slot.
var pageRetryGap = time.Minute

// Where a next page link or a load more element keeps its url, in order.
var pageURLAttrs = []string{"href", "data-href", "data-url", "data-next"}

type PageRule struct {
	Next      *string `json:"next" jsonschema:"nullable"`
	Template  *string `json:"template" jsonschema:"nullable"`
	Reasoning string  `json:"reasoning"`
}

func (PageRule) JSONSchemaExtend(s *jsonschema.Schema) {
	for name, desc := range prompts.pageFields {
		if p, ok := s.Properties.Get(name); ok {
			p.Description = desc
		}
	}
}

func (r PageRule) Abstained() bool {
	return r.Next == nil && r.Template == nil
}

func (r PageRule) sanitize() PageRule {
	trim := func(s *string) *string {
		if s == nil {
			return nil
		}

		v := strings.TrimSpace(*s)
		if v == "" || v == "null" {
			return nil
		}

		return &v
	}

	r.Next = trim(r.Next)
	r.Template = trim(r.Template)
	r.Reasoning = truncate(collapse(r.Reasoning), 200)

	return r
}

// A template is an absolute url learned on one listing, and another source on the domain has its own
// listing. So it only applies from the listing it was learned on, and then from its own page n-1.
type storedPageRule struct {
	PageRule
	Listing string `json:"listing"`
}

func (r storedPageRule) appliesTo(cur string, n int) bool {
	if r.Template == nil {
		return true
	}

	if n == 2 {
		return cur == r.Listing
	}

	return strings.ReplaceAll(*r.Template, pageNumber, strconv.Itoa(n-1)) == cur
}

type candidate struct {
	tag       string
	classes   []string
	rel       string
	ariaLabel string
	text      string
	url       string
}

// Pagination often sits in nav or a button, which parse prunes, so pages read the unpruned body.
func rawBody(page []byte) (*nethtml.Node, error) {
	root, err := nethtml.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, fmt.Errorf("err parse page: %w", err)
	}

	body := find(root, func(n *nethtml.Node) bool { return n.DataAtom == atom.Body })
	if body == nil {
		return nil, errNoBody
	}

	return body, nil
}

func pageURL(n *nethtml.Node, base *url.URL) string {
	for _, a := range pageURLAttrs {
		v := strings.TrimSpace(attr(n, a))
		if v == "" {
			continue
		}

		u, err := base.Parse(v)
		if err != nil || u.Host != base.Host || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}

		u.Fragment = ""

		return u.String()
	}

	return ""
}

// pageCandidates lists the elements on a listing that may lead to its next page: same-host urls that are
// neither the listing itself nor an article the links rule finds.
func pageCandidates(listing sample, links LinkRule) []candidate {
	base, err := url.Parse(listing.URL)
	if err != nil {
		return nil
	}

	body, err := rawBody(listing.Data)
	if err != nil {
		return nil
	}

	seen := map[string]bool{base.String(): true}

	if articles, err := applyLinks(links, listing); err == nil {
		for _, a := range articles {
			seen[a] = true
		}
	}

	var out []candidate

	walk(body, func(n *nethtml.Node) bool {
		if len(out) >= maxCandidates {
			return false
		}

		if n.Type != nethtml.ElementNode {
			return true
		}

		u := pageURL(n, base)
		if u == "" || seen[u] {
			return true
		}

		seen[u] = true

		var cls []string
		for _, c := range classes(n) {
			if !hashed(c) {
				cls = append(cls, c)
			}
		}

		out = append(out, candidate{
			tag:       n.Data,
			classes:   cls,
			rel:       attr(n, "rel"),
			ariaLabel: attr(n, "aria-label"),
			text:      truncate(collapse(text(n)), candidateText),
			url:       u,
		})

		return true
	})

	return out
}

func candidateVocabulary(cands []candidate) vocabulary {
	v := newVocabulary()

	for _, c := range cands {
		v.tags[c.tag] = true

		for _, cl := range c.classes {
			v.classes[cl] = true
		}

		if c.rel != "" {
			v.addAttr("rel", c.rel)
		}

		if c.ariaLabel != "" {
			v.addAttr("aria-label", c.ariaLabel)
		}
	}

	return v
}

// applyPage gives the url of listing page n, or "" when the rule finds none on the listing's host.
func applyPage(rule PageRule, listing sample, n int) (string, error) {
	if rule.Abstained() {
		return "", ErrAbstained
	}

	base, err := url.Parse(listing.URL)
	if err != nil {
		return "", fmt.Errorf("extract: parse url %s: %w", listing.URL, err)
	}

	var next string

	if rule.Template != nil {
		u, err := base.Parse(strings.ReplaceAll(*rule.Template, pageNumber, strconv.Itoa(n)))
		if err != nil || u.Host != base.Host {
			return "", nil
		}

		next = u.String()
	} else {
		sel, err := cascadia.Parse(*rule.Next)
		if err != nil {
			return "", fmt.Errorf("extract: compile %q: %w", *rule.Next, err)
		}

		body, err := rawBody(listing.Data)
		if err != nil {
			return "", err
		}

		for _, m := range cascadia.QueryAll(body, sel) {
			if next = pageURL(m, base); next != "" {
				break
			}
		}
	}

	if next == base.String() {
		return "", nil
	}

	return next, nil
}

// A rule passes when page 2 brings new articles, and a next rule moves on again from page 2.
func checkPageRule(ctx context.Context, rule PageRule, links LinkRule, listing sample, fetch fetchFunc) error {
	if rule.Abstained() {
		return ErrAbstained
	}

	if rule.Template != nil && !strings.Contains(*rule.Template, pageNumber) {
		return fmt.Errorf("extract: template %q has no %s", *rule.Template, pageNumber)
	}

	second, err := applyPage(rule, listing, 2)
	if err != nil {
		return err
	}

	if second == "" {
		return errors.New("extract: no page 2")
	}

	page, err := fetch(ctx, second)
	if err != nil {
		return fmt.Errorf("extract: fetch page 2 %s: %w", second, err)
	}

	first, err := applyLinks(links, listing)
	if err != nil {
		return err
	}

	next := sample{URL: second, Data: page}

	got, err := applyLinks(links, next)
	if err != nil {
		return err
	}

	if len(got) < minLinks {
		return fmt.Errorf("extract: page 2 %s: %d links, at least %d needed", second, len(got), minLinks)
	}

	known := make(map[string]bool, len(first))
	for _, l := range first {
		known[l] = true
	}

	fresh := 0
	for _, l := range got {
		if !known[l] {
			fresh++
		}
	}

	if 2*fresh < len(got) {
		return fmt.Errorf("extract: page 2 %s: %d of %d links are new", second, fresh, len(got))
	}

	if rule.Next == nil {
		return nil
	}

	third, err := applyPage(rule, next, 3)
	if err != nil {
		return err
	}

	if third == "" || third == second || third == listing.URL {
		return fmt.Errorf("extract: page 3 %q does not move on from page 2", third)
	}

	return nil
}

func (t *htmlExtractor) generatePageRule(ctx context.Context, l *slog.Logger, listing sample, links LinkRule, fetch fetchFunc) (PageRule, error) {
	cands := pageCandidates(listing, links)
	if len(cands) == 0 {
		return PageRule{}, fmt.Errorf("%w: no candidates on %s", ErrNoRule, listing.URL)
	}

	vocab := candidateVocabulary(cands)

	req := port.Request{
		System: prompts.pagesSystem,
		Prompt: prompts.candidates(cands),
		Model:  t.model,
	}

	errs := []error{ErrNoRule}

	for _, temp := range temperatures {
		req.Temperature = &temp

		var rule PageRule

		if err := t.llm.Unmarshal(ctx, l, req, &rule); err != nil {
			if errors.Is(err, port.ErrUnavailable) || ctx.Err() != nil {
				return PageRule{}, err
			}

			errs = append(errs, err)
			continue
		}

		rule = rule.sanitize()

		if rule.Next != nil {
			if err := vocab.ground(*rule.Next); err != nil {
				errs = append(errs, err)
				continue
			}
		}

		err := checkPageRule(ctx, rule, links, listing, fetch)
		if ctx.Err() != nil {
			return PageRule{}, ctx.Err()
		}

		if err != nil {
			errs = append(errs, err)
			continue
		}

		return rule, nil
	}

	return PageRule{}, errors.Join(errs...)
}

// Next gives the url of listing page n, the page after url. Without a rule it starts learning one
// in the background and gives "" meanwhile. It needs the links rule to check a rule.
func (t *htmlExtractor) Next(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte, n int) (string, error) {
	s := sample{URL: url, Data: listing}

	if rule, ok := ruleOf[storedPageRule](d, fieldPages); ok {
		if !rule.appliesTo(url, n) {
			return "", nil
		}

		return applyPage(rule.PageRule, s, n)
	}

	links, ok := ruleOf[LinkRule](d, fieldLinks)
	if !ok {
		return "", nil
	}

	t.learnPages(ctx, l, d, s, links)

	return "", nil
}

// learnPages keeps one goroutine per domain waiting for off-peak and a free slot, since nothing else
// asks again until the next backfill.
func (t *htmlExtractor) learnPages(ctx context.Context, l *slog.Logger, d domain.Domain, s sample, links LinkRule) {
	t.mu.Lock()
	skip := t.failing(fieldPages, d.ID) || t.learning[d.ID]
	if !skip {
		t.learning[d.ID] = true
	}
	t.mu.Unlock()

	if skip {
		return
	}

	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.learning, d.ID)
			t.mu.Unlock()
		}()

		for {
			if t.llm.Schedule().Active(time.Now()) && t.tryLearnPages(ctx, l, d, s, links) {
				return
			}

			select {
			case <-time.After(pageRetryGap):
			case <-ctx.Done():
				return
			}
		}
	}()
}

// tryLearnPages is false when the slot was busy or DeepSeek unavailable, so the caller tries again.
func (t *htmlExtractor) tryLearnPages(ctx context.Context, l *slog.Logger, d domain.Domain, s sample, links LinkRule) bool {
	select {
	case t.generating <- struct{}{}:
	default:
		return false
	}
	defer func() { <-t.generating }()

	rule, err := t.generatePageRule(ctx, l, s, links, t.fetcher(l))

	switch {
	case ctx.Err() != nil:
		return true
	case errors.Is(err, port.ErrUnavailable):
		return false
	case err != nil:
		t.mu.Lock()
		t.fail(fieldPages, d.ID)
		t.mu.Unlock()

		t.l.Warn("generate page rule", "domain", d.BaseURL, "err", err)

		return true
	}

	if err := t.saveRule(ctx, d.ID, fieldPages, storedPageRule{PageRule: rule, Listing: s.URL}); err != nil {
		t.l.Error("save page rule", "domain", d.BaseURL, "err", err)
		return true
	}

	t.l.Info("page rule generated", "domain", d.BaseURL, "reasoning", rule.Reasoning)

	return true
}
