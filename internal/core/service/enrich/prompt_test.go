package enrich

import (
	"fmt"
	"strings"
	"testing"
)

func TestSystemPrompt(t *testing.T) {
	for _, s := range []string{"summary", "tags", "events:"} {
		if !strings.Contains(systemPrompt, s) {
			t.Errorf("system prompt misses %q", s)
		}
	}
}

func TestUserPrompt(t *testing.T) {
	keys := make([]string, 400)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
	}

	p, err := userPrompt("sv", keys, []string{"acquisition"}, strings.Repeat("å", 25_000))
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range []string{"Language: sv", "k299", "Quality tag keys: acquisition"} {
		if !strings.Contains(p, s) {
			t.Errorf("user prompt misses %q", s)
		}
	}

	if strings.Contains(p, "k300") || strings.Count(p, "å") != 20_000 {
		t.Errorf("caps not applied: k300 %v, runes %d", strings.Contains(p, "k300"), strings.Count(p, "å"))
	}
}
