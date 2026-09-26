package html

import (
	"errors"
	"fmt"
	"strings"

	"github.com/andybalholm/cascadia"
	"github.com/invopop/jsonschema"
)

const maxExclude = 12

const (
	ImageKeep = "keep"
	ImageDrop = "drop"
)

type Rule struct {
	Include     *string  `json:"include" jsonschema:"nullable"`
	Exclude     []string `json:"exclude" jsonschema:"maxItems=12"`
	StripAfter  *string  `json:"strip_after" jsonschema:"nullable"`
	Title       *string  `json:"title" jsonschema:"nullable"`
	Published   *string  `json:"published" jsonschema:"nullable"`
	Author      *string  `json:"author" jsonschema:"nullable"`
	ImagePolicy string   `json:"image_policy" jsonschema:"enum=keep,enum=drop"`
	Reasoning   string   `json:"reasoning"`
}

func (Rule) JSONSchemaExtend(s *jsonschema.Schema) {
	for name, desc := range prompts.fields {
		if p, ok := s.Properties.Get(name); ok {
			p.Description = desc
		}
	}
}

func (r Rule) Abstained() bool {
	return r.Include == nil || strings.TrimSpace(*r.Include) == ""
}

func (r Rule) selectors() (out []string) {
	for _, s := range []*string{r.Include, r.StripAfter, r.Title, r.Published, r.Author} {
		if s != nil {
			out = append(out, *s)
		}
	}

	return append(out, r.Exclude...)
}

func (r Rule) sanitize() Rule {
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

	r.Include = trim(r.Include)
	r.StripAfter = trim(r.StripAfter)
	r.Title = trim(r.Title)
	r.Published = trim(r.Published)
	r.Author = trim(r.Author)

	exclude := make([]string, 0, len(r.Exclude))
	for _, e := range r.Exclude {
		if e = strings.TrimSpace(e); e != "" {
			exclude = append(exclude, e)
		}
	}

	r.Exclude = exclude

	if r.ImagePolicy != ImageDrop {
		r.ImagePolicy = ImageKeep
	}

	r.Reasoning = truncate(collapse(r.Reasoning), 200)

	return r
}

func (r Rule) check() error {
	if r.Abstained() {
		return errors.New("trimmer: rule has no include")
	}

	if len(r.Exclude) > maxExclude {
		return fmt.Errorf("trimmer: %d excludes, at most %d", len(r.Exclude), maxExclude)
	}

	for _, s := range r.selectors() {
		if _, err := parseSelector(s); err != nil {
			return err
		}
	}

	return nil
}

func (r Rule) ground(v vocabulary) error {
	if err := r.check(); err != nil {
		return err
	}

	for _, s := range r.selectors() {
		if err := v.ground(s); err != nil {
			return err
		}
	}

	return nil
}

type compiledRule struct {
	rule       Rule
	include    cascadia.Sel
	exclude    []cascadia.Sel
	stripAfter cascadia.Sel
	title      cascadia.Sel
	published  cascadia.Sel
	author     cascadia.Sel
}

func compile(r Rule) (c *compiledRule, err error) {
	if err = r.check(); err != nil {
		return
	}

	c = &compiledRule{rule: r}

	opt := func(s *string) (cascadia.Sel, error) {
		if s == nil {
			return nil, nil
		}

		return cascadia.Parse(*s)
	}

	if c.include, err = cascadia.Parse(*r.Include); err != nil {
		return nil, err
	}

	for _, e := range r.Exclude {
		sel, err := cascadia.Parse(e)
		if err != nil {
			return nil, err
		}

		c.exclude = append(c.exclude, sel)
	}

	if c.stripAfter, err = opt(r.StripAfter); err != nil {
		return nil, err
	}

	if c.title, err = opt(r.Title); err != nil {
		return nil, err
	}

	if c.published, err = opt(r.Published); err != nil {
		return nil, err
	}

	if c.author, err = opt(r.Author); err != nil {
		return nil, err
	}

	return c, nil
}
