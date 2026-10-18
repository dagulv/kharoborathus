package robot

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const articleID = "article"

//go:embed prompts/article.md
var articlePrompt string

var articleCriteria = adapter.NoulCriteria{
	True:  "It reports one concrete event at a named company or organisation, like a deal, a result, an appointment, an order or a filing.",
	False: "It is a listing, category, product, profile, guide, opinion or login page, or it mentions events only in passing.",
}

type Verdict struct {
	Article float64            `json:"article"`
	Tags    map[string]float64 `json:"tags"`
	Keys    []string           `json:"-"`
	Passed  bool               `json:"-"`
}

func articleQuestion() map[string]adapter.NoulQuestion {
	return map[string]adapter.NoulQuestion{
		articleID: {Instructions: strings.TrimSpace(articlePrompt), Criteria: &articleCriteria},
	}
}

func (t *Tagger) Gate(ctx context.Context, l *slog.Logger, title, markdown string, tags []Tag) (Verdict, error) {
	scores, extra, err := t.classify(ctx, l, title, markdown, tags, articleQuestion())
	if err != nil {
		return Verdict{}, err
	}

	v := Verdict{Article: extra[articleID], Tags: scores, Keys: matched(tags, scores)}
	v.Passed = v.Article >= tagThreshold && slices.ContainsFunc(tags, func(tag Tag) bool {
		return tag.Gate && scores[tag.Key] >= tagThreshold
	})

	return v, nil
}

func (t *Tagger) Quality(ctx context.Context, l *slog.Logger, title, markdown string) (bool, error) {
	_, extra, err := t.classify(ctx, l, title, markdown, nil, articleQuestion())
	if err != nil {
		return false, fmt.Errorf("err classify quality: %w", err)
	}

	return extra[articleID] >= tagThreshold, nil
}
