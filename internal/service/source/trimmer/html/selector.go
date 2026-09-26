package html

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/andybalholm/cascadia"
)

const (
	maxSelectorLen       = 200
	maxSelectorCompounds = 6
)

var (
	ErrSelectorSyntax     = errors.New("trimmer: selector syntax")
	ErrSelectorUngrounded = errors.New("trimmer: selector not grounded in the observed dom")
)

var deniedAttrs = map[string]bool{
	"class": true,
	"id":    true,
	"style": true,
}

type attrRef struct {
	name  string
	value string
	has   bool
}

type selectorRefs struct {
	tags    []string
	classes []string
	ids     []string
	attrs   []attrRef
}

func parseSelector(s string) (refs selectorRefs, err error) {
	if s == "" || len(s) > maxSelectorLen {
		return refs, fmt.Errorf("%w: length %d", ErrSelectorSyntax, len(s))
	}

	p := selectorParser{s: strings.TrimSpace(s)}

	for compounds := 0; ; compounds++ {
		if compounds == maxSelectorCompounds {
			return refs, fmt.Errorf("%w: more than %d compounds in %q", ErrSelectorSyntax, maxSelectorCompounds, s)
		}

		if err = p.compound(&refs); err != nil {
			return refs, fmt.Errorf("%w: %w in %q", ErrSelectorSyntax, err, s)
		}

		if p.done() {
			break
		}

		if err = p.combinator(); err != nil {
			return refs, fmt.Errorf("%w: %w in %q", ErrSelectorSyntax, err, s)
		}
	}

	if _, err = cascadia.Compile(s); err != nil {
		return refs, fmt.Errorf("%w: %w", ErrSelectorSyntax, err)
	}

	return refs, nil
}

type selectorParser struct {
	s   string
	pos int
}

func (p *selectorParser) done() bool {
	return p.pos >= len(p.s)
}

func (p *selectorParser) peek() byte {
	if p.done() {
		return 0
	}

	return p.s[p.pos]
}

func (p *selectorParser) compound(refs *selectorRefs) error {
	start := p.pos

	if isIdentStart(p.peek()) {
		refs.tags = append(refs.tags, strings.ToLower(p.ident()))
	}

	for !p.done() {
		switch p.peek() {
		case '.':
			p.pos++
			name := p.ident()
			if name == "" {
				return errors.New("empty class")
			}

			refs.classes = append(refs.classes, name)
		case '#':
			p.pos++
			name := p.ident()
			if name == "" {
				return errors.New("empty id")
			}

			refs.ids = append(refs.ids, name)
		case '[':
			a, err := p.attribute()
			if err != nil {
				return err
			}

			refs.attrs = append(refs.attrs, a)
		case ' ', '\t', '\n', '>', '+', '~':
			if p.pos == start {
				return errors.New("empty compound")
			}

			return nil
		default:
			return fmt.Errorf("unexpected %q", p.peek())
		}
	}

	if p.pos == start {
		return errors.New("empty compound")
	}

	return nil
}

func (p *selectorParser) combinator() error {
	var explicit bool

	for !p.done() {
		switch c := p.peek(); {
		case c == ' ' || c == '\t' || c == '\n':
			p.pos++
		case (c == '>' || c == '+' || c == '~') && !explicit:
			explicit = true
			p.pos++
		default:
			return nil
		}
	}

	return errors.New("dangling combinator")
}

func (p *selectorParser) attribute() (a attrRef, err error) {
	p.pos++

	if a.name = strings.ToLower(p.ident()); a.name == "" {
		return a, errors.New("empty attribute")
	}

	switch {
	case p.peek() == ']':
		p.pos++
		return a, nil
	case p.peek() == '=':
		p.pos++
	case strings.HasPrefix(p.s[p.pos:], "~="):
		p.pos += 2
	default:
		return a, fmt.Errorf("unsupported attribute operator at %q", p.s[p.pos:])
	}

	a.has = true

	if q := p.peek(); q == '"' || q == '\'' {
		end := strings.IndexByte(p.s[p.pos+1:], q)
		if end < 0 {
			return a, errors.New("unterminated attribute value")
		}

		a.value = p.s[p.pos+1 : p.pos+1+end]
		p.pos += end + 2

		if strings.ContainsAny(a.value, `\"'`) {
			return a, errors.New("escaped attribute value")
		}
	} else if a.value = p.ident(); a.value == "" {
		return a, errors.New("empty attribute value")
	}

	if p.peek() != ']' {
		return a, errors.New("unterminated attribute")
	}

	p.pos++

	return a, nil
}

func (p *selectorParser) ident() string {
	start := p.pos

	for !p.done() && isIdent(p.peek()) {
		p.pos++
	}

	return p.s[start:p.pos]
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdent(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c == '-' || c == '_'
}

type vocabulary struct {
	tags    map[string]bool
	classes map[string]bool
	ids     map[string]bool
	attrs   map[string]map[string]bool
}

func newVocabulary() vocabulary {
	return vocabulary{
		tags:    make(map[string]bool),
		classes: make(map[string]bool),
		ids:     make(map[string]bool),
		attrs:   make(map[string]map[string]bool),
	}
}

func (v vocabulary) addAttr(name, value string) {
	if v.attrs[name] == nil {
		v.attrs[name] = make(map[string]bool)
	}

	v.attrs[name][value] = true
}

func (v vocabulary) ground(sel string) error {
	refs, err := parseSelector(sel)
	if err != nil {
		return err
	}

	for _, t := range refs.tags {
		if !v.tags[t] {
			return fmt.Errorf("%w: tag %q in %q", ErrSelectorUngrounded, t, sel)
		}
	}

	for _, c := range refs.classes {
		if hashed(c) || !v.classes[c] {
			return fmt.Errorf("%w: class %q in %q", ErrSelectorUngrounded, c, sel)
		}
	}

	for _, id := range refs.ids {
		if hashed(id) || !v.ids[id] {
			return fmt.Errorf("%w: id %q in %q", ErrSelectorUngrounded, id, sel)
		}
	}

	for _, a := range refs.attrs {
		values, ok := v.attrs[a.name]
		if deniedAttrs[a.name] || !ok {
			return fmt.Errorf("%w: attribute %q in %q", ErrSelectorUngrounded, a.name, sel)
		}

		if a.has && (hashed(a.value) || !values[a.value]) {
			return fmt.Errorf("%w: attribute value %s=%q in %q", ErrSelectorUngrounded, a.name, a.value, sel)
		}
	}

	return nil
}

var hashedPrefixes = []string{"css-", "sc-", "jsx-", "svelte-", "emotion-", "styled-", "astro-", "tw-"}

func hashed(name string) bool {
	lower := strings.ToLower(name)

	for _, p := range hashedPrefixes {
		if strings.HasPrefix(lower, p) && len(lower) > len(p)+3 {
			return true
		}
	}

	if i := strings.LastIndex(name, "__"); i > 0 && mixed(name[i+2:]) {
		return true
	}

	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' }) {
		if len(seg) >= 5 && transitions(seg) >= 2 {
			return true
		}
	}

	return false
}

func mixed(s string) bool {
	return len(s) >= 4 && strings.IndexFunc(s, unicode.IsDigit) >= 0 && strings.IndexFunc(s, unicode.IsLetter) >= 0
}

func transitions(s string) (n int) {
	var prev, cur int

	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			cur = 1
		case unicode.IsLetter(r):
			cur = 2
		default:
			cur = 0
		}

		if prev != 0 && cur != 0 && prev != cur {
			n++
		}

		prev = cur
	}

	return
}
