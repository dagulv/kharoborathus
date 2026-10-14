package html

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
)

func pages(s site, from, n int) []trimmer.Sample {
	out := make([]trimmer.Sample, 0, n)

	for i := from; i < from+n; i++ {
		out = append(out, trimmer.Sample{URL: s.url(i), Data: []byte(s.article(i))})
	}

	return out
}

func TestValidate(t *testing.T) {
	s := site{body: "article-body", updated: "Uppdaterad 08:12"}
	heldOut := pages(s, 10, 5)

	removed, err := validate(mustCompile(t, goodRule()), heldOut)
	if err != nil || removed <= 0 {
		t.Fatalf("good rule: removed %.2f, %v", removed, err)
	}

	greedy := goodRule()
	greedy.Exclude = append(greedy.Exclude, "p + p + p + p + p")

	if _, err := validate(mustCompile(t, greedy), heldOut); err == nil || !strings.Contains(err.Error(), "paragraphs") {
		t.Errorf("truncating rule: %v", err)
	}

	lazy := goodRule()
	lazy.Exclude = []string{".updated"}

	if _, err := validate(mustCompile(t, lazy), heldOut); err == nil {
		t.Error("rule that removes nothing passed")
	}

	if _, err := validate(mustCompile(t, goodRule()), heldOut[:4]); err == nil {
		t.Error("passed on fewer than the minimum held out pages")
	}
}

func TestGenerateRuleset(t *testing.T) {
	s := site{body: "article-body", updated: "x"}

	invented := goodRule()
	invented.Include = str("div.story-body")

	weaker := goodRule()
	weaker.Exclude = []string{".promo", ".updated"}

	llm := &fakeLLM{answers: []any{weaker, invented, goodRule()}}
	tr := New(llm, "")

	rule, err := tr.GenerateRuleset(context.Background(), pages(s, 0, 8))
	if err != nil {
		t.Fatal(err)
	}

	if len(rule.Exclude) != len(goodRule().Exclude) {
		t.Errorf("picked %v, want the rule removing the most boilerplate", rule.Exclude)
	}

	if p := llm.prompts[0]; !strings.Contains(p, "=== page 3 ===") || strings.Contains(p, "=== page 4 ===") || strings.Contains(p, "Artikel 1, stycke") {
		t.Errorf("prompt should hold three skeletons and no article text:\n%s", p)
	}

	llm = &fakeLLM{answers: []any{invented, Rule{Include: nil, Reasoning: "no clear container"}}}

	if _, err := New(llm, "").GenerateRuleset(context.Background(), pages(s, 0, 8)); !errors.Is(err, trimmer.ErrNoRule) ||
		!errors.Is(err, ErrSelectorUngrounded) || !errors.Is(err, ErrAbstained) {
		t.Errorf("no usable candidate: %v", err)
	}

	if _, err := tr.GenerateRuleset(context.Background(), pages(s, 0, 7)); err == nil {
		t.Error("generated without enough held out pages")
	}
}
