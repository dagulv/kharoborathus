package html

import (
	"bytes"
	"cmp"
	"errors"
	"strings"
	"unicode/utf8"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var denylist = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Noscript: true,
	atom.Template: true,
	atom.Iframe:   true,
	atom.Svg:      true,
	atom.Math:     true,
	atom.Canvas:   true,
	atom.Nav:      true,
	atom.Footer:   true,
	atom.Button:   true,
	atom.Input:    true,
	atom.Select:   true,
	atom.Textarea: true,
	atom.Object:   true,
	atom.Embed:    true,
	atom.Link:     true,
	atom.Meta:     true,
	atom.Dialog:   true,
}

var deniedRoles = map[string]bool{
	"navigation":  true,
	"contentinfo": true,
	"search":      true,
	"dialog":      true,
	"alertdialog": true,
}

var invisible = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Noscript: true,
	atom.Template: true,
	atom.Head:     true,
}

var blockLevel = map[atom.Atom]bool{
	atom.Address: true, atom.Article: true, atom.Aside: true, atom.Blockquote: true,
	atom.Br: true, atom.Dd: true, atom.Details: true, atom.Div: true, atom.Dl: true,
	atom.Dt: true, atom.Fieldset: true, atom.Figcaption: true, atom.Figure: true,
	atom.Footer: true, atom.Form: true, atom.H1: true, atom.H2: true, atom.H3: true,
	atom.H4: true, atom.H5: true, atom.H6: true, atom.Header: true, atom.Hr: true,
	atom.Li: true, atom.Main: true, atom.Nav: true, atom.Ol: true, atom.P: true,
	atom.Pre: true, atom.Section: true, atom.Summary: true, atom.Table: true,
	atom.Td: true, atom.Th: true, atom.Tr: true, atom.Ul: true,
}

var errNoBody = errors.New("trimmer: document has no body")

type document struct {
	root *nethtml.Node
	body *nethtml.Node
	meta map[string]string
}

func parse(page []byte) (doc document, err error) {
	root, err := nethtml.Parse(bytes.NewReader(page))
	if err != nil {
		return
	}

	doc.root = root
	doc.body = find(root, func(n *nethtml.Node) bool { return n.DataAtom == atom.Body })

	if doc.body == nil {
		return doc, errNoBody
	}

	doc.meta = metadata(root)

	prune(doc.body)

	return doc, nil
}

func metadata(root *nethtml.Node) map[string]string {
	m := make(map[string]string)

	walk(root, func(n *nethtml.Node) bool {
		switch n.DataAtom {
		case atom.Meta:
			key := cmp.Or(attr(n, "property"), attr(n, "name"), attr(n, "itemprop"))
			if key != "" && m[key] == "" {
				m[key] = collapse(attr(n, "content"))
			}
		case atom.Title:
			if m["title"] == "" {
				m["title"] = collapse(text(n))
			}
		}

		return true
	})

	return m
}

func prune(n *nethtml.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling

		if denied(c) {
			n.RemoveChild(c)
		} else {
			prune(c)
		}

		c = next
	}
}

func denied(n *nethtml.Node) bool {
	switch n.Type {
	case nethtml.CommentNode, nethtml.DoctypeNode:
		return true
	case nethtml.ElementNode:
	default:
		return false
	}

	if denylist[n.DataAtom] || n.Namespace == "svg" || n.Namespace == "math" {
		return true
	}

	if deniedRoles[attr(n, "role")] || attr(n, "aria-hidden") == "true" {
		return true
	}

	return hasAttr(n, "hidden")
}

func walk(n *nethtml.Node, visit func(*nethtml.Node) bool) {
	if !visit(n) {
		return
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

func find(n *nethtml.Node, match func(*nethtml.Node) bool) (found *nethtml.Node) {
	walk(n, func(n *nethtml.Node) bool {
		if found != nil {
			return false
		}

		if n.Type == nethtml.ElementNode && match(n) {
			found = n
			return false
		}

		return true
	})

	return
}

func attr(n *nethtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}

	return ""
}

func hasAttr(n *nethtml.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return true
		}
	}

	return false
}

func classes(n *nethtml.Node) []string {
	return strings.Fields(attr(n, "class"))
}

func text(n *nethtml.Node) string {
	var b strings.Builder

	walk(n, func(n *nethtml.Node) bool {
		if n.Type == nethtml.ElementNode && invisible[n.DataAtom] {
			return false
		}

		if n.Type == nethtml.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}

		return true
	})

	return b.String()
}

func directText(n *nethtml.Node) string {
	var b strings.Builder

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == nethtml.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
	}

	return collapse(b.String())
}

func textLen(n *nethtml.Node) int {
	return utf8.RuneCountInString(collapse(text(n)))
}

func linkLen(n *nethtml.Node) (l int) {
	walk(n, func(n *nethtml.Node) bool {
		if n.Type == nethtml.ElementNode && n.DataAtom == atom.A {
			l += textLen(n)
			return false
		}

		return true
	})

	return
}

func linkDensity(n *nethtml.Node) float64 {
	total := textLen(n)
	if total == 0 {
		return 0
	}

	return float64(linkLen(n)) / float64(total)
}

func blocks(n *nethtml.Node) []string {
	var (
		out []string
		cur strings.Builder
	)

	flush := func() {
		if s := collapse(cur.String()); s != "" {
			out = append(out, s)
		}

		cur.Reset()
	}

	var visit func(*nethtml.Node)
	visit = func(n *nethtml.Node) {
		switch n.Type {
		case nethtml.TextNode:
			cur.WriteString(n.Data)
			return
		case nethtml.ElementNode:
			if invisible[n.DataAtom] {
				return
			}
		}

		block := n.Type == nethtml.ElementNode && blockLevel[n.DataAtom]
		if block {
			flush()
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}

		if block {
			flush()
		}
	}

	visit(n)
	flush()

	return out
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func detach(n *nethtml.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}
