package enrich

import (
	"fmt"
	"strings"
	"testing"
)

func TestSystemPrompt(t *testing.T) {
	for _, s := range []string{"summary", "tags", "events:", "categories:", "quarterly_report", "entities:", "strategy", "No people"} {
		if !strings.Contains(systemPrompt, s) {
			t.Errorf("system prompt misses %q", s)
		}
	}

	if strings.Contains(systemPrompt, "earnings_report") {
		t.Error("system prompt still has earnings_report")
	}
}

func TestUserPrompt(t *testing.T) {
	keys := make([]string, 400)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
	}

	p, err := userPrompt("sv", "Volvo köper", []string{"TT", "Anna Berg"}, keys, []string{"acquisition"}, []string{"energy", "finance"}, strings.Repeat("å", 25_000), []string{"volvo_cars", "northvolt"})
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range []string{"Language: sv", "k299", "Quality tag keys: acquisition", "Categories: energy, finance", "Title: Volvo köper", "Authors: TT, Anna Berg", "Existing entity keys: volvo_cars, northvolt"} {
		if !strings.Contains(p, s) {
			t.Errorf("user prompt misses %q", s)
		}
	}

	if strings.Contains(p, "k300") || strings.Count(p, "å") != 20_000 {
		t.Errorf("caps not applied: k300 %v, runes %d", strings.Contains(p, "k300"), strings.Count(p, "å"))
	}
}

func TestUserPromptNoCategories(t *testing.T) {
	p, err := userPrompt("sv", "", nil, nil, nil, nil, "text", nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range []string{"Categories: none", "Authors: unknown", "Existing entity keys: none"} {
		if !strings.Contains(p, s) {
			t.Errorf("user prompt misses %q", s)
		}
	}
}
