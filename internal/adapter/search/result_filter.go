package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dagulv/kharoborathus/internal/core/port"
	"golang.org/x/sync/errgroup"
)

const (
	keepThreshold = 0.8
	filterWorkers = 5
)

var resultQuestion = map[string]port.NoulQuestion{
	"result": {
		Instructions: "Does this search result report the given action by the given entity?",
		Criteria: &port.NoulCriteria{
			True:  "It reports this same event by this same entity, as a news article, press release, company page or filing from any site, small or large.",
			False: "It reports another event, mentions the entity or event only in passing, is a listing, search, tag or category page, a stock quote or a company profile, or its text does not show the event.",
		},
	},
}

type SearchResult struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	ExtraSnippets []string `json:"extra_snippets,omitempty"`
	PageAge       string   `json:"page_age,omitempty"`
	Hostname      string   `json:"hostname"`
}

type ResultFilter struct {
	classifier port.Classifier
}

func NewResultFilter(c port.Classifier) *ResultFilter {
	return &ResultFilter{classifier: c}
}

// One Jev call per result, so no result is judged by another's text. A result Jev fails on is dropped,
// so one bad result cannot block its query. An outage, or failing on every result, fails the filter.
func (f *ResultFilter) Keep(ctx context.Context, l *slog.Logger, entity, action string, results []SearchResult) ([]int, error) {
	scores := make([]float64, len(results))
	errs := make([]error, len(results))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(filterWorkers)

	for i, r := range results {
		g.Go(func() error {
			state := map[string]string{
				"entity":         entity,
				"action":         action,
				"title":          r.Title,
				"description":    r.Description,
				"extra_snippets": strings.Join(r.ExtraSnippets, "\n"),
				"page_age":       r.PageAge,
				"hostname":       r.Hostname,
			}

			s, err := f.classifier.Noul(ctx, l, state, resultQuestion)
			if errors.Is(err, port.ErrUnavailable) {
				return fmt.Errorf("err classify search result %d: %w", i, err)
			}

			errs[i] = err
			scores[i] = s["result"]

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Each failed call is already a row in the logs table.
	failed := 0

	for _, err := range errs {
		if err != nil {
			failed++
		}
	}

	if failed > 0 && failed == len(results) {
		return nil, fmt.Errorf("err classify every search result: %w", errs[0])
	}

	var kept []int

	for i, s := range scores {
		if s >= keepThreshold {
			kept = append(kept, i)
		}
	}

	return kept, nil
}
