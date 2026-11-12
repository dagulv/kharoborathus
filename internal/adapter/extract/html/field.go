package html

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andybalholm/cascadia"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/invopop/jsonschema"
	nethtml "golang.org/x/net/html"
)

// The metadata key of each field's rule.
type field string

const (
	fieldLinks    field = "links"
	fieldPages    field = "pages"
	fieldContent  field = "content"
	fieldTitle    field = "title"
	fieldAuthors  field = "authors"
	fieldPostedAt field = "postedAt"
)

const (
	// Some articles lack a byline or a date, so a field rule need not hit every held out page.
	minFieldHits = 3
	minTitle     = 5
	maxTitle     = 300
	maxAuthors   = 10
	minAuthor    = 2
	maxAuthor    = 100
	// Zoneless times parse as UTC, which can run ahead of the site's clock.
	futureSlack = 24 * time.Hour
)

// An abstained rule is a stored answer: the site does not show the field.
type FieldRule struct {
	Selector  *string `json:"selector" jsonschema:"nullable"`
	Reasoning string  `json:"reasoning"`
}

func (FieldRule) JSONSchemaExtend(s *jsonschema.Schema) {
	for name, desc := range prompts.fieldFields {
		if p, ok := s.Properties.Get(name); ok {
			p.Description = desc
		}
	}
}

func (r FieldRule) Abstained() bool {
	return r.Selector == nil || strings.TrimSpace(*r.Selector) == ""
}

func (r FieldRule) sanitize() FieldRule {
	if r.Selector != nil {
		v := strings.TrimSpace(*r.Selector)
		r.Selector = &v

		if v == "" || v == "null" {
			r.Selector = nil
		}
	}

	r.Reasoning = truncate(collapse(r.Reasoning), 200)

	return r
}

// A rule that does not compile reads as abstained, so the field falls back.
func (r FieldRule) sel() cascadia.Sel {
	if r.Abstained() {
		return nil
	}

	if _, err := parseSelector(*r.Selector); err != nil {
		return nil
	}

	sel, err := cascadia.Parse(*r.Selector)
	if err != nil {
		return nil
	}

	return sel
}

func readTitle(doc document, rule FieldRule) string {
	return cmp.Or(first(doc.root, rule.sel()), first(doc.root, headline), doc.meta["og:title"], doc.meta["title"])
}

func readAuthors(doc document, rule FieldRule) []string {
	if authors := authorsOf(doc, rule.sel()); len(authors) > 0 {
		return authors
	}

	if a := cmp.Or(doc.meta["author"], doc.meta["article:author"]); a != "" {
		return []string{a}
	}

	return nil
}

func readPostedAt(doc document, rule FieldRule) time.Time {
	if t := postedAtOf(doc, rule.sel()); !t.IsZero() {
		return t
	}

	if t := postedAtOf(doc, publishedTime); !t.IsZero() {
		return t
	}

	return parseTime(doc.meta["article:published_time"], doc.meta["datePublished"])
}

func first(root *nethtml.Node, sel cascadia.Sel) string {
	if sel == nil {
		return ""
	}

	if n := cascadia.Query(root, sel); n != nil {
		return collapse(text(n))
	}

	return ""
}

func authorsOf(doc document, sel cascadia.Sel) (out []string) {
	if sel == nil {
		return nil
	}

	seen := make(map[string]bool)

	for _, n := range cascadia.QueryAll(doc.root, sel) {
		if a := collapse(text(n)); a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}

	return out
}

func postedAtOf(doc document, sel cascadia.Sel) time.Time {
	if sel == nil {
		return time.Time{}
	}

	n := cascadia.Query(doc.root, sel)
	if n == nil {
		return time.Time{}
	}

	return parseTime(attr(n, "datetime"), attr(n, "content"), collapse(text(n)))
}

var timeLayouts = []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02"}

func parseTime(raw ...string) time.Time {
	for _, r := range raw {
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, strings.TrimSpace(r)); err == nil {
				return t
			}
		}
	}

	return time.Time{}
}

// Checks the rule alone, without the fallbacks, so a rule that only finds meta tag values fails.
func validateField(f field, rule FieldRule, heldOut []sample) error {
	if len(heldOut) < minHeldOut {
		return fmt.Errorf("extract: %d held out pages, at least %d needed", len(heldOut), minHeldOut)
	}

	sel := rule.sel()
	if sel == nil {
		return fmt.Errorf("extract: %s selector does not compile", f)
	}

	hits := 0

	for _, p := range heldOut {
		doc, err := parse(p.Data)
		if err != nil {
			return fmt.Errorf("extract: parse %s: %w", p.URL, err)
		}

		found, err := checkFieldValue(f, doc, sel)
		if err != nil {
			return fmt.Errorf("extract: %s: %w", p.URL, err)
		}

		if found {
			hits++
		}
	}

	if hits < minFieldHits {
		return fmt.Errorf("extract: %s found on %d of %d held out pages, at least %d needed", f, hits, len(heldOut), minFieldHits)
	}

	return nil
}

func checkFieldValue(f field, doc document, sel cascadia.Sel) (found bool, err error) {
	switch f {
	case fieldTitle:
		t := first(doc.root, sel)
		if t == "" {
			return false, nil
		}

		if l := utf8.RuneCountInString(t); l < minTitle || l > maxTitle {
			return true, fmt.Errorf("title of %d characters", l)
		}
	case fieldAuthors:
		authors := authorsOf(doc, sel)
		if len(authors) == 0 {
			return false, nil
		}

		if len(authors) > maxAuthors {
			return true, fmt.Errorf("%d authors, at most %d", len(authors), maxAuthors)
		}

		for _, a := range authors {
			if l := utf8.RuneCountInString(a); l < minAuthor || l > maxAuthor {
				return true, fmt.Errorf("author %q of %d characters", truncate(a, 40), l)
			}
		}
	case fieldPostedAt:
		t := postedAtOf(doc, sel)
		if t.IsZero() {
			return false, nil
		}

		if t.After(time.Now().Add(futureSlack)) {
			return true, fmt.Errorf("posted at %s is in the future", t.Format(time.RFC3339))
		}
	default:
		return false, fmt.Errorf("no field %q", f)
	}

	return true, nil
}

// The first rule that passes wins. When none passes, an abstain is a valid answer.
func (t *htmlExtractor) generateField(ctx context.Context, l *slog.Logger, f field, samples []sample) (FieldRule, error) {
	skel, heldOut, err := sampleSkeleton(samples)
	if err != nil {
		return FieldRule{}, err
	}

	req := port.Request{
		System: prompts.fieldSystem(f),
		Prompt: prompts.user(skel),
		Model:  t.model,
	}

	var (
		abstained *FieldRule
		errs      = []error{ErrNoRule}
	)

	for _, temp := range temperatures {
		req.Temperature = &temp

		var rule FieldRule

		if err := t.llm.Unmarshal(ctx, l, req, &rule); err != nil {
			if errors.Is(err, port.ErrUnavailable) || ctx.Err() != nil {
				return FieldRule{}, err
			}

			errs = append(errs, err)
			continue
		}

		rule = rule.sanitize()

		if rule.Abstained() {
			abstained = &rule
			errs = append(errs, ErrAbstained)
			continue
		}

		if err := checkFieldRule(f, rule, skel, heldOut); err != nil {
			errs = append(errs, err)
			continue
		}

		return rule, nil
	}

	if abstained != nil {
		return *abstained, nil
	}

	return FieldRule{}, errors.Join(errs...)
}

func checkFieldRule(f field, rule FieldRule, skel Skeleton, heldOut []sample) error {
	if err := skel.vocab.ground(*rule.Selector); err != nil {
		return err
	}

	return validateField(f, rule, heldOut)
}
