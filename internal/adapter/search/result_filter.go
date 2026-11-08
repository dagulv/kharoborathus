package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/dagulv/kharoborathus/internal/core/port"
)

type SearchResult struct {
	URL           string   `json:"-"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	ExtraSnippets []string `json:"extra_snippets,omitempty"`
	PageAge       string   `json:"page_age,omitempty"`
	Hostname      string   `json:"hostname"`
}

type filterResult struct {
	ID int `json:"id"`
	SearchResult
}

type filterAnswer struct {
	Keep []int `json:"keep"`
}

type ResultFilter struct {
	llm port.LLM
}

func NewResultFilter(llm port.LLM) *ResultFilter {
	return &ResultFilter{llm: llm}
}

func (f *ResultFilter) Keep(ctx context.Context, l *slog.Logger, entity, action string, results []SearchResult) ([]int, error) {
	if len(results) == 0 {
		return nil, nil
	}

	ids := make([]filterResult, len(results))

	for i, r := range results {
		ids[i] = filterResult{ID: i, SearchResult: r}
	}

	b, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("err marshal search results: %w", err)
	}

	prompt, err := render("search_filter_user_prompt", map[string]any{
		"Entity":  entity,
		"Action":  action,
		"Results": string(b),
	})
	if err != nil {
		return nil, err
	}

	temp := 0.0

	var answer filterAnswer

	if err := f.llm.Unmarshal(ctx, l, port.Request{
		System:      searchFilterSystem,
		Prompt:      prompt,
		Temperature: &temp,
	}, &answer); err != nil {
		return nil, fmt.Errorf("err filter search results: %w", err)
	}

	var kept []int

	for _, id := range answer.Keep {
		if id < 0 || id >= len(results) || slices.ContainsFunc(kept, func(k int) bool { return results[k].URL == results[id].URL }) {
			continue
		}

		kept = append(kept, id)
	}

	return kept, nil
}
