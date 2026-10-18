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

func TestSkeletonSinglePage(t *testing.T) {
	d, err := parse([]byte(listing(0)))
	if err != nil {
		t.Fatal(err)
	}

	page := buildSkeleton([]document{d}).Pages[0]

	// Every text is on fewer than two captures, so the capture annotations would all claim unique.
	for _, denied := range []string{"[unique]", "[shared]", "[partial", "uniq:"} {
		if strings.Contains(page, denied) {
			t.Errorf("%s in single page skeleton:\n%s", denied, page)
		}
	}

	if !strings.Contains(page, `<article class="teaser"`) {
		t.Errorf("teasers missing:\n%s", page)
	}
}

func TestSkeletonKeepsDifferentSiblings(t *testing.T) {
	d, err := parse([]byte(`<html><body><main>
<details class="section"><summary>About</summary><p>Om mig och vad jag gör om dagarna.</p><a class="experience-link" href="/a">Jobb</a></details>
<details class="section"><summary>Work</summary><ul class="work"><li><a class="project" href="/b">Projekt ett</a></li><li><a class="project" href="/c">Projekt två</a></li></ul></details>
</main></body></html>`))
	if err != nil {
		t.Fatal(err)
	}

	page := buildSkeleton([]document{d}).Pages[0]

	// Same tag and class, different insides: collapsing them would hide the second.
	for _, want := range []string{"experience-link", `<ul class="work"`, `<li> links:1.00 children:1 ×2`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q in\n%s", want, page)
		}
	}

	if n := strings.Count(page, `<details class="section">`); n != 2 {
		t.Errorf("%d section lines, want 2:\n%s", n, page)
	}
}
