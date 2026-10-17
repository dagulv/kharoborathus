package robot

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

//go:embed prompts/search_filter.md
var searchFilterPrompt string

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

type filterPrompt struct {
	Entity  string         `json:"entity"`
	Action  string         `json:"action"`
	Results []filterResult `json:"results"`
}

type filterAnswer struct {
	Keep []int `json:"keep"`
}

type ResultFilter struct {
	llm adapter.LLM
}

func NewResultFilter(llm adapter.LLM) *ResultFilter {
	return &ResultFilter{llm: llm}
}

func (f *ResultFilter) Keep(ctx context.Context, entity, action string, results []SearchResult) ([]string, error) {
	if len(results) == 0 {
		return nil, nil
	}

	p := filterPrompt{Entity: entity, Action: action, Results: make([]filterResult, len(results))}

	for i, r := range results {
		p.Results[i] = filterResult{ID: i, SearchResult: r}
	}

	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("err marshal search results: %w", err)
	}

	temp := 0.0

	var answer filterAnswer

	if err := f.llm.Unmarshal(ctx, adapter.Request{
		System:      strings.TrimSpace(searchFilterPrompt),
		Prompt:      string(b),
		Temperature: &temp,
	}, &answer); err != nil {
		return nil, err
	}

	var urls []string

	for _, id := range answer.Keep {
		if id < 0 || id >= len(results) || slices.Contains(urls, results[id].URL) {
			continue
		}

		urls = append(urls, results[id].URL)
	}

	return urls, nil
}
