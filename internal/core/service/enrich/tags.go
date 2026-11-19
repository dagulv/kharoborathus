package enrich

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
)

const (
	tagThreshold      = 0.8
	categoryThreshold = 0.5
	tagsPerRequest    = 50
	maxTagState       = 60_000
	excerptLen        = 280
)

func jevTags(ctx context.Context, c port.Classifier, l *slog.Logger, lang, title, markdown string, categories []domain.Category, tags []domain.Tag) ([]string, error) {
	if r := []rune(markdown); len(r) > maxTagState {
		markdown = string(r[:maxTagState])
	}

	state := map[string]string{"title": title, "article": markdown}

	if len(categories) > 0 {
		questions := make([]string, len(categories))

		for i, cat := range categories {
			q := "Is this news article about " + cat.Key
			if term, ok := cat.Terms[lang]; ok {
				q += " (" + term + ")"
			}

			questions[i] = q + "?"
		}

		scores, err := askJev(ctx, c, l, state, "category", questions)
		if err != nil {
			return nil, fmt.Errorf("err classify categories: %w", err)
		}

		matched := make(map[string]bool, len(categories))

		for i, cat := range categories {
			matched[cat.Key] = scores[i] >= categoryThreshold
		}

		tags = slices.DeleteFunc(slices.Clone(tags), func(tag domain.Tag) bool {
			return len(tag.Categories) > 0 && !slices.ContainsFunc(tag.Categories, func(key string) bool { return matched[key] })
		})
	}

	questions := make([]string, len(tags))

	for i, tag := range tags {
		q := "Is this news article about " + tag.Key
		if len(tag.Terms) > 0 {
			q += " (" + strings.Join(tag.Terms, ", ") + ")"
		}

		questions[i] = q + "?"
	}

	scores, err := askJev(ctx, c, l, state, "tag", questions)
	if err != nil {
		return nil, fmt.Errorf("err classify tags: %w", err)
	}

	var keys []string

	for i, tag := range tags {
		if scores[i] >= tagThreshold {
			keys = append(keys, tag.Key)
		}
	}

	return keys, nil
}

// Scores line up with questions. A score Jev leaves out is 0.
func askJev(ctx context.Context, c port.Classifier, l *slog.Logger, state map[string]string, idPrefix string, questions []string) ([]float64, error) {
	out := make([]float64, 0, len(questions))

	for batch := range slices.Chunk(questions, tagsPerRequest) {
		asks := make(map[string]port.NoulQuestion, len(batch))

		for i, q := range batch {
			asks[fmt.Sprintf("%s_%d", idPrefix, i)] = port.NoulQuestion{Instructions: q}
		}

		scores, err := c.Noul(ctx, l, state, asks)
		if err != nil {
			return nil, err
		}

		for i := range batch {
			out = append(out, scores[fmt.Sprintf("%s_%d", idPrefix, i)])
		}
	}

	return out, nil
}

var (
	markdownImage  = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownLink   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	markdownMarker = regexp.MustCompile(`(?m)^\s*(#+|>|[-*+]|\d+\.)\s+|[*_` + "`" + `]`)
)

// Stands in for an empty summary.
func excerpt(markdown string) string {
	text := markdownImage.ReplaceAllString(markdown, "")
	text = markdownLink.ReplaceAllString(text, "$1")
	text = markdownMarker.ReplaceAllString(text, "")
	text = strings.Join(strings.Fields(text), " ")

	if r := []rune(text); len(r) > excerptLen {
		text = string(r[:excerptLen])
	}

	return text
}
