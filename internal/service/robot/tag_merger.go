package robot

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

//go:embed prompts/tag_merge.md
var tagMergePrompt string

var tagMergeQuestion = template.Must(template.New("tag_merge.md").
	Funcs(template.FuncMap{"join": strings.Join}).
	Parse(strings.TrimSpace(tagMergePrompt)))

var tagMergeCriteria = adapter.NoulCriteria{
	True:  "An article about one is always about the other, like investment and investments, or layoffs and staff_cuts.",
	False: "One is broader, narrower or only related, like acquisition and merger, or earnings and earnings_report.",
}

type TagMerger struct {
	classifier adapter.Classifier
}

func NewTagMerger(classifier adapter.Classifier) *TagMerger {
	return &TagMerger{classifier: classifier}
}

func (m *TagMerger) Duplicate(ctx context.Context, candidate Tag, existing []Tag) (string, error) {
	if len(existing) == 0 {
		return "", nil
	}

	questions := make(map[string]adapter.NoulQuestion, len(existing))

	for i, tag := range existing {
		var b strings.Builder

		if err := tagMergeQuestion.Execute(&b, tag); err != nil {
			return "", fmt.Errorf("err tag merge question: %w", err)
		}

		questions[fmt.Sprintf("tag_%d", i)] = adapter.NoulQuestion{Instructions: b.String(), Criteria: &tagMergeCriteria}
	}

	answers, err := m.classifier.Noul(ctx, map[string]any{"key": candidate.Key, "terms": candidate.Terms}, questions)
	if err != nil {
		return "", fmt.Errorf("err tag merge answer: %w", err)
	}

	key, best := "", tagThreshold

	for i, tag := range existing {
		if score := answers[fmt.Sprintf("tag_%d", i)]; score >= best {
			key, best = tag.Key, score
		}
	}

	return key, nil
}
