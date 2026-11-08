package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/dagulv/kharoborathus/internal/core/port"
)

const (
	maxTagKeys        = 300
	maxKeywords       = 5
	maxKeywordQuery   = 100
	maxKeywordEntity  = 80
	maxKeywordAction  = 30
	maxDiscoveryInput = 20_000
)

type Keyword struct {
	Entity string `json:"entity"`
	Action string `json:"action"`
	Query  string `json:"query"`
}

type discoveryAnswer struct {
	Keywords []Keyword `json:"keywords"`
}

type Discoverer struct {
	llm port.LLM
}

func NewDiscoverer(llm port.LLM) *Discoverer {
	return &Discoverer{llm: llm}
}

func (d *Discoverer) Keywords(ctx context.Context, l *slog.Logger, markdown string, tagKeys []string) ([]Keyword, error) {
	if r := []rune(markdown); len(r) > maxDiscoveryInput {
		markdown = string(r[:maxDiscoveryInput])
	}

	prompt, err := render("discovery_user_prompt", map[string]any{
		"Keys":    tagKeys[:min(len(tagKeys), maxTagKeys)],
		"Article": markdown,
	})
	if err != nil {
		return nil, err
	}

	temp := 0.2

	var answer discoveryAnswer

	if err := d.llm.Unmarshal(ctx, l, port.Request{
		System:      discoverySystem,
		Prompt:      prompt,
		Temperature: &temp,
	}, &answer); err != nil {
		return nil, fmt.Errorf("err extract keywords: %w", err)
	}

	return groundKeywords(answer.Keywords, markdown), nil
}

func groundKeywords(keywords []Keyword, article string) []Keyword {
	text := strings.ToLower(article)
	seen := make(map[string]bool)
	out := make([]Keyword, 0, len(keywords))

	for _, k := range keywords {
		k.Entity = collapse(k.Entity)
		k.Action = tagKey(k.Action)
		k.Query = collapse(k.Query)

		entity := strings.ToLower(k.Entity)
		query := strings.ToLower(k.Query)

		switch {
		case k.Entity == "" || k.Action == "" || k.Query == "":
			continue
		case utf8.RuneCountInString(k.Entity) > maxKeywordEntity,
			utf8.RuneCountInString(k.Action) > maxKeywordAction,
			utf8.RuneCountInString(k.Query) > maxKeywordQuery:
			continue
		case !strings.Contains(text, entity) || !strings.Contains(query, entity):
			continue
		case seen[query]:
			continue
		}

		seen[query] = true
		out = append(out, k)

		if len(out) == maxKeywords {
			break
		}
	}

	return out
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func tagKey(s string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(collapse(s)))
}
