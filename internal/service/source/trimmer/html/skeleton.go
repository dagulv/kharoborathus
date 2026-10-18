package html

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	skeletonDepth   = 12
	skeletonLines   = 400
	snippetLen      = 80
	longText        = 80
	skeletonIndent  = "  "
	minSkeletonPage = 3
)

var skeletonAttrs = []string{"data-testid", "itemprop", "role", "datetime"}

var keepEmpty = map[atom.Atom]bool{
	atom.Img: true, atom.Picture: true, atom.Figure: true, atom.Video: true, atom.Time: true,
}

type skelNode struct {
	tag      string
	classes  []string
	id       string
	attrs    [][2]string
	key      string
	text     string
	subText  int
	uniqText int
	links    float64
	children []*skelNode
	elements int
}

func (n *skelNode) sig() string {
	var b strings.Builder

	b.WriteString(n.tag)

	if n.id != "" {
		b.WriteString("#")
		b.WriteString(n.id)
	}

	for _, c := range n.classes {
		b.WriteString(".")
		b.WriteString(c)
	}

	for _, a := range n.attrs {
		b.WriteString("[")
		b.WriteString(a[0])
		b.WriteString("=")
		b.WriteString(a[1])
		b.WriteString("]")
	}

	return b.String()
}

type Skeleton struct {
	Pages []string
	vocab vocabulary
}

func buildSkeleton(docs []document) Skeleton {
	trees := make([]*skelNode, len(docs))

	for i, d := range docs {
		trees[i] = skeletonOf(d.body, "", 0)
	}

	presence := make(map[string]int)
	textPages := make(map[string]int)

	for _, t := range trees {
		keys := make(map[string]bool)
		texts := make(map[string]bool)

		visitSkel(t, func(n *skelNode) {
			keys[n.key] = true

			if n.text != "" {
				texts[n.text] = true
			}
		})

		for k := range keys {
			presence[k]++
		}

		for t := range texts {
			textPages[t]++
		}
	}

	for _, t := range trees {
		uniqueText(t, textPages)
	}

	s := Skeleton{vocab: newVocabulary()}

	for _, t := range trees {
		r := renderer{presence: presence, textPages: textPages, pages: len(docs), vocab: s.vocab}
		r.render(t, 0)

		if r.lines >= skeletonLines {
			r.b.WriteString("… truncated\n")
		}

		s.Pages = append(s.Pages, r.b.String())
	}

	return s
}

func skeletonOf(n *nethtml.Node, parentKey string, depth int) *skelNode {
	s := &skelNode{tag: n.Data, id: attr(n, "id"), text: directText(n)}

	for _, c := range classes(n) {
		if !hashed(c) {
			s.classes = append(s.classes, c)
		}
	}

	slices.Sort(s.classes)
	s.classes = slices.Compact(s.classes)

	if hashed(s.id) {
		s.id = ""
	}

	for _, a := range skeletonAttrs {
		if !hasAttr(n, a) {
			continue
		}

		v := attr(n, a)
		if a == "datetime" || hashed(v) || len(v) > 60 {
			v = ""
		}

		s.attrs = append(s.attrs, [2]string{a, v})
	}

	s.key = parentKey + "/" + s.sig()
	s.subText = textLen(n)

	if s.subText > 0 {
		s.links = linkDensity(n)
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != nethtml.ElementNode {
			continue
		}

		s.elements++

		if depth < skeletonDepth {
			s.children = append(s.children, skeletonOf(c, s.key, depth+1))
		}
	}

	return s
}

func visitSkel(n *skelNode, f func(*skelNode)) {
	f(n)

	for _, c := range n.children {
		visitSkel(c, f)
	}
}

func uniqueText(n *skelNode, textPages map[string]int) int {
	if n.text != "" && textPages[n.text] < 2 {
		n.uniqText = utf8.RuneCountInString(n.text)
	}

	for _, c := range n.children {
		n.uniqText += uniqueText(c, textPages)
	}

	if len(n.children) < n.elements && n.subText > n.uniqText {
		n.uniqText = n.subText
	}

	n.uniqText = min(n.uniqText, n.subText)

	return n.uniqText
}

type renderer struct {
	b         strings.Builder
	lines     int
	presence  map[string]int
	textPages map[string]int
	pages     int
	vocab     vocabulary
}

func (r *renderer) render(n *skelNode, depth int) {
	for i := 0; i < len(n.children); {
		c := n.children[i]

		run := 1
		for i+run < len(n.children) && n.children[i+run].sig() == c.sig() {
			run++
		}

		longest := 0
		for _, sib := range n.children[i : i+run] {
			longest = max(longest, utf8.RuneCountInString(sib.text))
		}

		if r.lines >= skeletonLines {
			return
		}

		if c.subText > 0 || keepEmpty[atom.Lookup([]byte(c.tag))] {
			r.line(c, depth, run, longest)
			r.render(c, depth+1)
		}

		i += run
	}
}

func (r *renderer) line(n *skelNode, depth, run, longest int) {
	r.lines++
	r.vocab.tags[n.tag] = true

	b := &r.b
	b.WriteString(strings.Repeat(skeletonIndent, depth))
	b.WriteString("<")
	b.WriteString(n.tag)

	if len(n.classes) > 0 {
		fmt.Fprintf(b, ` class="%s"`, strings.Join(n.classes, " "))

		for _, c := range n.classes {
			r.vocab.classes[c] = true
		}
	}

	if n.id != "" {
		fmt.Fprintf(b, ` id="%s"`, n.id)
		r.vocab.ids[n.id] = true
	}

	for _, a := range n.attrs {
		if a[1] == "" {
			fmt.Fprintf(b, " %s", a[0])
		} else {
			fmt.Fprintf(b, ` %s="%s"`, a[0], a[1])
		}

		r.vocab.addAttr(a[0], a[1])
	}

	b.WriteString(">")

	// One capture has nothing to compare, so every text would read as unique.
	switch count := r.presence[n.key]; {
	case r.pages == 1:
	case n.text != "" && r.textPages[n.text] < 2:
		b.WriteString(" [unique]")
	case count >= r.pages:
		b.WriteString(" [shared]")
	default:
		fmt.Fprintf(b, " [partial %d/%d]", count, r.pages)
	}

	if longest > 0 {
		if longest >= longText {
			fmt.Fprintf(b, " text:%d+", longText)
		} else {
			fmt.Fprintf(b, " text:%d", longest)
		}

		if r.textPages[n.text] >= 2 {
			fmt.Fprintf(b, " %q", truncate(n.text, snippetLen))
		}
	}

	if n.subText > 0 && len(n.children) > 0 && r.pages > 1 {
		fmt.Fprintf(b, " uniq:%.2f", float64(n.uniqText)/float64(n.subText))
	}

	if n.subText > 0 {
		fmt.Fprintf(b, " links:%.2f", n.links)
	}

	if len(n.children) < n.elements {
		fmt.Fprintf(b, " children:%d pruned", n.elements)
	} else if n.elements > 0 {
		fmt.Fprintf(b, " children:%d", n.elements)
	}

	if run > 1 {
		fmt.Fprintf(b, " ×%d", run)
	}

	b.WriteByte('\n')
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	r := []rune(s)

	return string(r[:n]) + "…"
}
