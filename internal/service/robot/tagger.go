package robot

import (
	"context"
	_ "embed"
	"fmt"
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
}

type Tagger struct {
	classifier adapter.Classifier
}

func NewTagger(classifier adapter.Classifier) *Tagger {
	return &Tagger{classifier: classifier}
}

func (t *Tagger) Tags(ctx context.Context, title, markdown string, tags []Tag) ([]string, error) {
	if r := []rune(markdown); len(r) > maxTagState {
		markdown = string(r[:maxTagState])
	}

	state := map[string]string{"title": title, "article": markdown}

	var keys []string

	for start := 0; start < len(tags); start += tagsPerRequest {
		batch := tags[start:min(start+tagsPerRequest, len(tags))]
		questions := make(map[string]adapter.NoulQuestion, len(batch))

		for i, tag := range batch {
			var b strings.Builder

			if err := tagQuestion.Execute(&b, tag); err != nil {
				return nil, err
			}

			questions[fmt.Sprintf("tag_%d", i)] = adapter.NoulQuestion{Instructions: b.String()}
		}

		answers, err := t.classifier.Noul(ctx, state, questions)
		if err != nil {
			return nil, err
		}

		for i, tag := range batch {
			if answers[fmt.Sprintf("tag_%d", i)] >= tagThreshold {
				keys = append(keys, tag.Key)
			}
		}
	}

	return keys, nil
}
