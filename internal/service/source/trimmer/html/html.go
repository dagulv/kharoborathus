package html

import (
	"bytes"
	"cmp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/andybalholm/cascadia"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	minOutput      = 200
	maxOutput      = 200_000
	maxLinkDensity = 0.25
	maxPageShare   = 0.6
)

const (
	FailParse         = "parse"
	FailIncludeNone   = "include_none"
	FailIncludeMany   = "include_many"
	FailTooShort      = "too_short"
	FailTooLong       = "too_long"
	FailLinkDensity   = "link_density"
	FailPageShare     = "page_share"
	FailPaywall       = "paywall"
	FailTitleRepeated = "title_repeated"
	FailUnterminated  = "unterminated"
)

var paywallMarkers = []string{
	"subscribe to continue",
	"subscribe to read",
	"to continue reading",
	"already a subscriber",
	"subscribers only",
	"for subscribers",
	"log in to read",
	"sign in to read",
	"this article is for",
	"prenumerera för att läsa",
	"för att läsa vidare",
	"för att fortsätta läsa",
	"redan prenumerant",
	"bli prenumerant",
	"logga in för att läsa",
	"endast för prenumeranter",
	"för prenumeranter",
	"läs hela artikeln",
}

type extraction struct {
	trimmer.Extraction

	pageText    int
	linkDensity float64
	paragraphs  []string
}

var _ trimmer.Trimmer[Rule] = (*htmlTrimmer)(nil)

type htmlTrimmer struct {
	llm   adapter.LLM
	model string
}

func New(llm adapter.LLM, model string) trimmer.Trimmer[Rule] {
	return &htmlTrimmer{llm: llm, model: cmp.Or(model, adapter.ModelFlash)}
}

func (t *htmlTrimmer) Trim(rule Rule, page []byte) (trimmer.Extraction, error) {
	if rule.Abstained() {
		return trimGeneric(page).Extraction, nil
	}

	c, err := compile(rule)
	if err != nil {
		return trimmer.Extraction{}, err
	}

	return trimRule(c, page).Extraction, nil
}

func trimRule(c *compiledRule, page []byte) (e extraction) {
	doc, err := parse(page)
	if err != nil {
		return extraction{Failures: []string{FailParse}}
	}

	matches := cascadia.QueryAll(doc.body, c.include)
	e.pageText = doc.pageText

	e.Title = cmp.Or(first(doc.root, c.title), doc.meta["og:title"], doc.meta["title"])
	e.Author = cmp.Or(first(doc.root, c.author), doc.meta["author"], doc.meta["article:author"])
	e.Published = published(doc, c.published)

	switch {
	case len(matches) == 0:
		e.Failures = append(e.Failures, FailIncludeNone)
		sanity(&e)
		return e
	case len(matches) > 1:
		e.Failures = append(e.Failures, FailIncludeMany)
	}

	content := matches[0]

	for _, sel := range c.exclude {
		for _, n := range cascadia.QueryAll(content, sel) {
			if n != content {
				detach(n)
			}
		}
	}

	if c.stripAfter != nil {
		if n := cascadia.Query(content, c.stripAfter); n != nil && n != content {
			stripAfter(content, n)
		}
	}

	if c.rule.ImagePolicy == ImageDrop {
		dropImages(content)
	}

	output(&e, content)
	sanity(&e)

	return e
}

func trimGeneric(page []byte) (e extraction) {
	doc, err := parse(page)
	if err != nil {
		return extraction{Failures: []string{FailParse}}
	}

	e.pageText = doc.pageText
	e.Title = cmp.Or(first(doc.root, headline), doc.meta["og:title"], doc.meta["title"])
	e.Author = cmp.Or(doc.meta["author"], doc.meta["article:author"])
	e.Published = published(doc, publishedTime)

	content := densest(doc.body)

	for _, n := range cascadia.QueryAll(content, genericExclude) {
		if linkDensity(n) > 0.5 || n.DataAtom == atom.Aside {
			detach(n)
		}
	}

	output(&e, content)
	sanity(&e)

	return e
}

var (
	headline       = mustParse("h1")
	publishedTime  = mustParse("time[datetime]")
	genericExclude = cascadia.MustCompile("aside, ul, ol, div, section, figure")
	images         = cascadia.MustCompile("img, picture, figure, video")
)

func mustParse(s string) cascadia.Sel {
	sel, err := cascadia.Parse(s)
	if err != nil {
		panic(err)
	}

	return sel
}

func densest(body *nethtml.Node) *nethtml.Node {
	scores := make(map[*nethtml.Node]float64)

	walk(body, func(n *nethtml.Node) bool {
		if n.Type != nethtml.ElementNode {
			return true
		}

		if n.DataAtom != atom.P && n.DataAtom != atom.Pre && n.DataAtom != atom.Blockquote {
			return true
		}

		l := textLen(n)
		if l < 25 {
			return false
		}

		score := 1 + float64(strings.Count(text(n), ",")) + min(float64(l)/100, 3)

		if p := n.Parent; p != nil {
			scores[p] += score

			if g := p.Parent; g != nil {
				scores[g] += score / 2
			}
		}

		return false
	})

	best, bestScore := body, 0.0

	for n, s := range scores {
		if s *= 1 - linkDensity(n); s > bestScore {
			best, bestScore = n, s
		}
	}

	return best
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

var timeLayouts = []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02"}

func published(doc document, sel cascadia.Sel) time.Time {
	var raw []string

	if sel != nil {
		if n := cascadia.Query(doc.root, sel); n != nil {
			raw = append(raw, attr(n, "datetime"), attr(n, "content"), collapse(text(n)))
		}
	}

	raw = append(raw, doc.meta["article:published_time"], doc.meta["datePublished"])

	for _, r := range raw {
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, strings.TrimSpace(r)); err == nil {
				return t
			}
		}
	}

	return time.Time{}
}

func stripAfter(root, n *nethtml.Node) {
	for cur := n; cur != nil && cur != root; cur = cur.Parent {
		for s := cur.NextSibling; s != nil; {
			next := s.NextSibling
			detach(s)
			s = next
		}
	}

	detach(n)
}

func dropImages(n *nethtml.Node) {
	for _, img := range cascadia.QueryAll(n, images) {
		detach(img)
	}
}

var outputAttrs = map[string]bool{"href": true, "src": true, "alt": true, "title": true, "datetime": true, "colspan": true, "rowspan": true}

func output(e *extraction, content *nethtml.Node) {
	walk(content, func(n *nethtml.Node) bool {
		if n.Type != nethtml.ElementNode {
			return true
		}

		attrs := n.Attr[:0]

		for _, a := range n.Attr {
			if a.Namespace == "" && outputAttrs[a.Key] && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:") {
				attrs = append(attrs, a)
			}
		}

		n.Attr = attrs

		return true
	})

	var b bytes.Buffer

	for c := content.FirstChild; c != nil; c = c.NextSibling {
		_ = nethtml.Render(&b, c)
	}

	e.Content = strings.TrimSpace(b.String())
	e.Text = strings.Join(blocks(content), "\n\n")
	e.paragraphs = paragraphs(content)
	e.linkDensity = linkDensity(content)
}

func sanity(e *extraction) {
	l := e.Len()

	switch {
	case l < minOutput:
		e.Failures = append(e.Failures, FailTooShort)
	case l > maxOutput:
		e.Failures = append(e.Failures, FailTooLong)
	}

	if l == 0 {
		return
	}

	if e.linkDensity >= maxLinkDensity {
		e.Failures = append(e.Failures, FailLinkDensity)
	}

	if e.pageText > 0 && float64(l) > maxPageShare*float64(e.pageText) {
		e.Failures = append(e.Failures, FailPageShare)
	}

	lower := strings.ToLower(e.Text)
	for _, m := range paywallMarkers {
		if strings.Contains(lower, m) {
			e.Failures = append(e.Failures, FailPaywall)
			break
		}
	}

	if e.Title != "" {
		firstLine, _, _ := strings.Cut(e.Text, "\n")
		if normalize(firstLine) == normalize(e.Title) {
			e.Failures = append(e.Failures, FailTitleRepeated)
		}
	}

	if !terminated(e.Text) {
		e.Failures = append(e.Failures, FailUnterminated)
	}
}

func terminated(s string) bool {
	s = strings.TrimRightFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(`"'”’»)]`, r)
	})

	r, _ := utf8.DecodeLastRuneInString(s)

	return strings.ContainsRune(".!?…", r)
}

func normalize(s string) string {
	return strings.ToLower(collapse(strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			return ' '
		}

		return r
	}, s)))
}

func paragraphs(content *nethtml.Node) (out []string) {
	walk(content, func(n *nethtml.Node) bool {
		if n.Type == nethtml.ElementNode && n.DataAtom == atom.P {
			if p := normalize(text(n)); utf8.RuneCountInString(p) >= 20 {
				out = append(out, p)
			}

			return false
		}

		return true
	})

	return
}

func recall(candidate, generic extraction) float64 {
	have := normalize(candidate.Text)

	var total, kept int

	for _, p := range generic.paragraphs {
		l := utf8.RuneCountInString(p)
		total += l

		if strings.Contains(have, p) {
			kept += l
		}
	}

	if total == 0 {
		return 1
	}

	return float64(kept) / float64(total)
}
