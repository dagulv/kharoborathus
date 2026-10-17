package robot

import (
	"context"
	"slices"
	"strings"
	"testing"
)

var searchResults = []SearchResult{
	{URL: "https://a.example/1", Title: "Volvo köper Northvolt", Hostname: "a.example"},
	{URL: "https://b.example/2", Title: "Volvo-aktien", Hostname: "b.example"},
	{URL: "https://c.example/3", Title: "Volvo förvärvar Northvolt", Description: "Affären", ExtraSnippets: []string{"Mer"}, PageAge: "2026-10-16T08:00:00", Hostname: "c.example"},
}

func TestKeep(t *testing.T) {
	llm := &fakeLLM{answer: filterAnswer{Keep: []int{2, 0, 2, 7, -1}}}

	got, err := NewResultFilter(llm).Keep(context.Background(), "Volvo", "acquisition", searchResults)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"https://c.example/3", "https://a.example/1"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	for _, s := range []string{`"entity":"Volvo"`, `"action":"acquisition"`, `"id":2`, `"extra_snippets":["Mer"]`, `"page_age":"2026-10-16T08:00:00"`, `"hostname":"c.example"`} {
		if !strings.Contains(llm.req.Prompt, s) {
			t.Errorf("prompt misses %s: %s", s, llm.req.Prompt)
		}
	}

	if strings.Contains(llm.req.Prompt, "https://") || llm.req.System == "" {
		t.Errorf("prompt %s", llm.req.Prompt)
	}
}

func TestKeepNoResults(t *testing.T) {
	llm := &fakeLLM{}

	got, err := NewResultFilter(llm).Keep(context.Background(), "Volvo", "acquisition", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}

	if llm.req.System != "" {
		t.Error("model called without results")
	}
}
