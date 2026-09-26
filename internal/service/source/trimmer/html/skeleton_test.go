package html

import (
	"strings"
	"testing"
)

func TestSkeleton(t *testing.T) {
	s := site{body: "article-body", updated: "Uppdaterad 08:12"}

	var docs []document
	for i := range 3 {
		d, err := parse([]byte(s.article(i)))
		if err != nil {
			t.Fatal(err)
		}

		docs = append(docs, d)
	}

	skel := buildSkeleton(docs)
	page := skel.Pages[0]

	line := func(prefix string) string {
		for _, l := range strings.Split(page, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), prefix) {
				return l
			}
		}

		t.Fatalf("no line %q in\n%s", prefix, page)
		return ""
	}

	if l := line(`<div class="article-body"`); !strings.Contains(l, "[shared]") || !strings.Contains(l, "uniq:0.8") {
		t.Errorf("content container: %s", l)
	}

	if l := line("<p> [unique]"); !strings.Contains(l, "×8") || strings.Contains(l, "Artikel") {
		t.Errorf("paragraphs should be collapsed and unquoted: %s", l)
	}

	if l := line(`<div class="newsletter-cta"`); !strings.Contains(l, `"Få morgonens`) {
		t.Errorf("template text should be quoted: %s", l)
	}

	if l := line(`<aside class="related"`); !strings.Contains(l, "uniq:0.00") {
		t.Errorf("related: %s", l)
	}

	for _, denied := range []string{"<nav", "<footer", "<script"} {
		if strings.Contains(page, denied) {
			t.Errorf("denylisted %s in skeleton", denied)
		}
	}

	if !skel.vocab.classes["article-body"] || skel.vocab.classes["menu"] {
		t.Errorf("vocabulary should hold rendered names only: %v", skel.vocab.classes)
	}
}
