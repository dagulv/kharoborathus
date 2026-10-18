package robot

import (
	"context"
	_ "embed"
	"log/slog"
	"regexp"
	"strings"
	"text/template"
	"unicode/utf8"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	maxGeneratedTags  = 5
	maxTagKey         = 40
	maxTagTerm        = 60
	maxExistingTags   = 300
	maxGeneratorInput = 20_000
)

//go:embed prompts/tag_generate.md
var tagGeneratePrompt string

//go:embed prompts/tag_generate_user.md
var tagGenerateUserPrompt string

var tagGenerateUser = template.Must(template.New("tag_generate_user.md").
	Funcs(template.FuncMap{"join": strings.Join}).
	Parse(tagGenerateUserPrompt))

var tagKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

type GeneratedTag struct {
	Key  string `json:"key"`
	Term string `json:"term"`
}

type tagGenerateAnswer struct {
	Tags []GeneratedTag `json:"tags"`
}

type TagGenerator struct {
	llm adapter.LLM
}

func NewTagGenerator(llm adapter.LLM) *TagGenerator {
	return &TagGenerator{llm: llm}
}

func (g *TagGenerator) Generate(ctx context.Context, l *slog.Logger, lang, markdown string, existing []string) ([]GeneratedTag, error) {
	if r := []rune(markdown); len(r) > maxGeneratorInput {
		markdown = string(r[:maxGeneratorInput])
	}

	var prompt strings.Builder

	if err := tagGenerateUser.Execute(&prompt, map[string]any{
		"Lang":    lang,
		"Keys":    existing[:min(len(existing), maxExistingTags)],
		"Article": markdown,
	}); err != nil {
		return nil, err
	}

	temp := 0.2

	var answer tagGenerateAnswer

	if err := g.llm.Unmarshal(ctx, l, adapter.Request{
		System:      strings.TrimSpace(tagGeneratePrompt),
		Prompt:      prompt.String(),
		Temperature: &temp,
	}, &answer); err != nil {
		return nil, err
	}

	return cleanTags(answer.Tags), nil
}

func TagKey(s string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(collapse(s)))
}

func cleanTags(tags []GeneratedTag) []GeneratedTag {
	seen := make(map[string]bool)
	out := make([]GeneratedTag, 0, len(tags))

	for _, t := range tags {
		t.Key = TagKey(t.Key)
		t.Term = strings.ToLower(collapse(t.Term))

		switch {
		case !tagKeyPattern.MatchString(t.Key) || len(t.Key) > maxTagKey:
			continue
		case t.Term == "" || utf8.RuneCountInString(t.Term) > maxTagTerm:
			continue
		case seen[t.Key]:
			continue
		}

		seen[t.Key] = true
		out = append(out, t)

		if len(out) == maxGeneratedTags {
			break
		}
	}

	return out
}
