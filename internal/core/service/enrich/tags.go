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
	tagThreshold   = 0.8
	tagsPerRequest = 50
	maxTagState    = 60_000
	excerptLen     = 280
)

func jevTags(ctx context.Context, c port.Classifier, l *slog.Logger, title, markdown string, tags []domain.Tag) ([]string, error) {
	if r := []rune(markdown); len(r) > maxTagState {
		markdown = string(r[:maxTagState])
	}

	state := map[string]string{"title": title, "article": markdown}

	var keys []string

	for batch := range slices.Chunk(tags, tagsPerRequest) {
		questions := make(map[string]port.NoulQuestion, len(batch))

		for i, tag := range batch {
			q := "Is this news article about " + tag.Key
			if len(tag.Terms) > 0 {
				q += " (" + strings.Join(tag.Terms, ", ") + ")"
			}

			questions[fmt.Sprintf("tag_%d", i)] = port.NoulQuestion{Instructions: q + "?"}
		}

		scores, err := c.Noul(ctx, l, state, questions)
		if err != nil {
			return nil, fmt.Errorf("err classify tags: %w", err)
		}

		for i, tag := range batch {
			if scores[fmt.Sprintf("tag_%d", i)] >= tagThreshold {
				keys = append(keys, tag.Key)
			}
		}
	}

	return keys, nil
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
