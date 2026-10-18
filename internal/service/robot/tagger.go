package robot

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"text/template"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	tagThreshold   = 0.8
	tagsPerRequest = 50
	maxTagState    = 60_000
)

//go:embed prompts/tag.md
var tagPrompt string

var tagQuestion = template.Must(template.New("tag.md").
	Funcs(template.FuncMap{"join": strings.Join}).
	Parse(strings.TrimSpace(tagPrompt)))

type Tag struct {
	Key   string
	Terms []string
	Gate  bool
}

type Tagger struct {
	classifier adapter.Classifier
}

func NewTagger(classifier adapter.Classifier) *Tagger {
	return &Tagger{classifier: classifier}
}

func (t *Tagger) Tags(ctx context.Context, l *slog.Logger, title, markdown string, tags []Tag) ([]string, error) {
	scores, _, err := t.classify(ctx, l, title, markdown, tags, nil)
	if err != nil {
		return nil, err
	}

	return matched(tags, scores), nil
}

// Extra questions ride in the first batch, so they cost no extra request.
func (t *Tagger) classify(ctx context.Context, l *slog.Logger, title, markdown string, tags []Tag, extra map[string]adapter.NoulQuestion) (scores, extraScores map[string]float64, err error) {
	if r := []rune(markdown); len(r) > maxTagState {
		markdown = string(r[:maxTagState])
	}

	state := map[string]string{"title": title, "article": markdown}
	scores = make(map[string]float64, len(tags))

	for start := 0; start == 0 || start < len(tags); start += tagsPerRequest {
		batch := tags[start:min(start+tagsPerRequest, len(tags))]
		questions := make(map[string]adapter.NoulQuestion, len(batch)+len(extra))

		for i, tag := range batch {
			var b strings.Builder

			if err := tagQuestion.Execute(&b, tag); err != nil {
				return nil, nil, err
			}

			questions[fmt.Sprintf("tag_%d", i)] = adapter.NoulQuestion{Instructions: b.String()}
		}

		if start == 0 {
			maps.Copy(questions, extra)
		}

		if len(questions) == 0 {
			break
		}

		answers, err := t.classifier.Noul(ctx, l, state, questions)
		if err != nil {
			return nil, nil, err
		}

		for i, tag := range batch {
			scores[tag.Key] = answers[fmt.Sprintf("tag_%d", i)]
		}

		if start == 0 {
			extraScores = answers
		}
	}

	return scores, extraScores, nil
}

func matched(tags []Tag, scores map[string]float64) []string {
	var keys []string

	for _, tag := range tags {
		if scores[tag.Key] >= tagThreshold {
			keys = append(keys, tag.Key)
		}
	}

	return keys
}
