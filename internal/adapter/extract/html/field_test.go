package html

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

func TestReadFields(t *testing.T) {
	doc, err := parse([]byte(site{body: "article-body"}.article(4)))
	if err != nil {
		t.Fatal(err)
	}

	if got := readAuthors(doc, FieldRule{Selector: str(".byline a")}); !slices.Equal(got, []string{"Anna Svensson"}) {
		t.Errorf("authors %v", got)
	}

	if got := readPostedAt(doc, FieldRule{Selector: str("time[datetime]")}); got.Day() != 5 {
		t.Errorf("posted at %v", got)
	}

	if got := readTitle(doc, FieldRule{Selector: str("h1.headline")}); got != "Rubrik nummer 4 om inflationen" {
		t.Errorf("title %q", got)
	}

	if got := readTitle(doc, FieldRule{Selector: str("h2.none")}); got != "Rubrik nummer 4 om inflationen" {
		t.Errorf("title without a match should fall back to h1, got %q", got)
	}

	if got := readAuthors(doc, FieldRule{}); got != nil {
		t.Errorf("abstained authors without meta: %v", got)
	}
}

// Only the first credited pages keep the byline class.
func credited(s site, from, n, keep int) []sample {
	out := pages(s, from, n)

	for i := keep; i < n; i++ {
		out[i].Data = []byte(strings.Replace(string(out[i].Data), `class="byline"`, `class="credit"`, 1))
	}

	return out
}

func TestValidateField(t *testing.T) {
	s := site{body: "article-body"}
	heldOut := pages(s, 10, 5)

	for f, sel := range map[field]string{fieldTitle: "h1.headline", fieldAuthors: ".byline a", fieldPostedAt: "time[datetime]"} {
		if err := validateField(f, FieldRule{Selector: str(sel)}, heldOut); err != nil {
			t.Errorf("%s %q: %v", f, sel, err)
		}
	}

	if err := validateField(fieldAuthors, FieldRule{Selector: str(".byline a")}, credited(s, 10, 5, 3)); err != nil {
		t.Errorf("authors on 3 of 5 pages: %v", err)
	}

	if err := validateField(fieldAuthors, FieldRule{Selector: str(".byline a")}, credited(s, 10, 5, 2)); err == nil {
		t.Error("authors on 2 of 5 pages passed")
	}

	if err := validateField(fieldTitle, FieldRule{Selector: str("h1.headline a")}, heldOut); err == nil {
		t.Error("title matching nothing passed")
	}

	future := pages(s, 10, 5)
	for i := range future {
		future[i].Data = []byte(strings.ReplaceAll(string(future[i].Data), "2026-09-", "2099-09-"))
	}

	if err := validateField(fieldPostedAt, FieldRule{Selector: str("time[datetime]")}, future); err == nil || !strings.Contains(err.Error(), "future") {
		t.Errorf("future posted at: %v", err)
	}
}

func TestGenerateField(t *testing.T) {
	s := site{body: "article-body"}
	good := FieldRule{Selector: str(".byline a"), Reasoning: "byline link"}
	invented := FieldRule{Selector: str(".writer a")}
	abstain := FieldRule{Reasoning: "no authors shown"}

	llm := &fakeLLM{answers: []any{abstain, good}}

	rule, err := newExtractor(llm, nil, nil, nil, slog.New(slog.DiscardHandler)).generateField(context.Background(), slog.New(slog.DiscardHandler), fieldAuthors, pages(s, 0, 8))
	if err != nil || rule.Abstained() || *rule.Selector != *good.Selector {
		t.Fatalf("rule %+v, %v", rule, err)
	}

	if !strings.Contains(llm.systems[0], "author names") {
		t.Errorf("system prompt should name the field:\n%s", llm.systems[0])
	}

	llm = &fakeLLM{answers: []any{abstain, invented}}

	rule, err = newExtractor(llm, nil, nil, nil, slog.New(slog.DiscardHandler)).generateField(context.Background(), slog.New(slog.DiscardHandler), fieldAuthors, pages(s, 0, 8))
	if err != nil || !rule.Abstained() || rule.Reasoning != abstain.Reasoning {
		t.Errorf("abstain should be a stored answer: %+v, %v", rule, err)
	}

	llm = &fakeLLM{answers: []any{invented}}

	if _, err := newExtractor(llm, nil, nil, nil, slog.New(slog.DiscardHandler)).generateField(context.Background(), slog.New(slog.DiscardHandler), fieldAuthors, pages(s, 0, 8)); !errors.Is(err, ErrNoRule) || !errors.Is(err, ErrSelectorUngrounded) {
		t.Errorf("no usable candidate: %v", err)
	}
}
