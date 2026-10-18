package html

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
)

const listingURL = "https://www.nyheterna.se/"

func listing(i int) string {
	var cards strings.Builder

	for n := i; n < i+8; n++ {
		fmt.Fprintf(&cards, `<article class="teaser"><h2 class="teaser-title"><a href="/artikel/%d">Rubrik nummer %d om %s</a></h2><p class="teaser-lead">Ingress till artikel %d.</p></article>`+"\n", n, n, subjects[n%len(subjects)], n)
	}

	return fmt.Sprintf(`<!doctype html>
<html><body>
<header class="site-header"><a href="/">Nyheterna</a></header>
<div class="sections"><a href="/inrikes">Inrikes nyheter</a><a href="/utrikes">Utrikes nyheter</a><a href="/ekonomi">Ekonomi nyheter</a><a href="/sport">Sport nyheter</a><a href="/kultur">Kultur nyheter</a></div>
<main class="feed">
%s<a class="more" href="/?sida=2">Fler nyheter</a>
</main>
</body></html>`, cards.String())
}

func listings(n int) []trimmer.Sample {
	out := make([]trimmer.Sample, 0, n)

	for i := range n {
		out = append(out, trimmer.Sample{URL: listingURL, Data: []byte(listing(i * 2))})
	}

	return out
}

func fetchSite(_ context.Context, url string) ([]byte, error) {
	id, ok := strings.CutPrefix(url, listingURL+"artikel/")
	if !ok {
		return []byte(`<html><body><h1>Sektion</h1><a href="/x">Länk</a></body></html>`), nil
	}

	i, err := strconv.Atoi(id)
	if err != nil {
		return nil, err
	}

	return []byte(site{body: "article-body"}.article(i)), nil
}

func TestLinks(t *testing.T) {
	links, err := NewLinker(&fakeLLM{}, "").Links(LinkRule{Links: str("article.teaser h2 a")}, listings(1)[0])
	if err != nil {
		t.Fatal(err)
	}

	if len(links) != 8 || links[0] != listingURL+"artikel/0" || links[7] != listingURL+"artikel/7" {
		t.Errorf("links: %v", links)
	}

	if _, err := NewLinker(&fakeLLM{}, "").Links(LinkRule{}, listings(1)[0]); !errors.Is(err, ErrAbstained) {
		t.Errorf("abstained rule: %v", err)
	}
}

func TestGenerateLinkRule(t *testing.T) {
	invented := LinkRule{Links: str(".story a")}
	sections := LinkRule{Links: str(".sections a")}
	good := LinkRule{Links: str("article.teaser h2 a"), Reasoning: "repeated teasers"}

	llm := &fakeLLM{answers: []any{invented, sections, good}}

	rule, err := NewLinker(llm, "").GenerateRuleset(context.Background(), listings(3), fetchSite)
	if err != nil {
		t.Fatal(err)
	}

	if *rule.Links != *good.Links {
		t.Errorf("picked %q, want %q", *rule.Links, *good.Links)
	}

	if p := llm.prompts[0]; !strings.Contains(p, "=== page 3 ===") || !strings.Contains(p, "teaser") {
		t.Errorf("prompt should hold three listing skeletons:\n%s", p)
	}

	llm = &fakeLLM{answers: []any{invented, sections, LinkRule{Reasoning: "no list"}}}

	_, err = NewLinker(llm, "").GenerateRuleset(context.Background(), listings(3), fetchSite)
	if !errors.Is(err, trimmer.ErrNoRule) || !errors.Is(err, ErrSelectorUngrounded) || !errors.Is(err, ErrAbstained) ||
		!strings.Contains(err.Error(), "linked pages are articles") {
		t.Errorf("no usable candidate: %v", err)
	}

	if _, err := NewLinker(llm, "").GenerateRuleset(context.Background(), listings(0), fetchSite); err == nil {
		t.Error("generated from no listings")
	}
}

func TestGenerateLinkRuleSingleListing(t *testing.T) {
	good := LinkRule{Links: str("article.teaser h2 a"), Reasoning: "repeated teasers"}
	llm := &fakeLLM{answers: []any{good}}

	rule, err := NewLinker(llm, "").GenerateRuleset(context.Background(), listings(1), fetchSite)
	if err != nil {
		t.Fatal(err)
	}

	if *rule.Links != *good.Links {
		t.Errorf("picked %q, want %q", *rule.Links, *good.Links)
	}

	if s := llm.systems[0]; !strings.Contains(s, "one capture") || strings.Contains(s, "[unique]") {
		t.Errorf("system prompt should describe a single capture:\n%s", s)
	}

	multi := &fakeLLM{answers: []any{good}}
	if _, err := NewLinker(multi, "").GenerateRuleset(context.Background(), listings(3), fetchSite); err != nil {
		t.Fatal(err)
	}

	if s := multi.systems[0]; !strings.Contains(s, "[unique]") {
		t.Errorf("system prompt should describe the capture annotations:\n%s", s)
	}
}

func TestLinkPrompts(t *testing.T) {
	if prompts.linksSystem(1) == "" || prompts.linksSystem(3) == "" {
		t.Fatal("empty links system prompt")
	}

	rt := reflect.TypeFor[LinkRule]()
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if prompts.linkFields[name] == "" {
			t.Errorf("no description for %q in prompts/link_rule.md", name)
		}
	}
}
