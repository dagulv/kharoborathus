package robot

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

//go:embed prompts/tag_merge.md
var tagMergePrompt string

type mergeAnswer struct {
	Same bool `json:"same"`
}

type TagMerger struct {
	llm adapter.LLM
}

func NewTagMerger(llm adapter.LLM) *TagMerger {
	return &TagMerger{llm: llm}
}

func (m *TagMerger) Same(ctx context.Context, a, b Tag) (bool, error) {
	temp := 0.0

	var answer mergeAnswer

	if err := m.llm.Unmarshal(ctx, adapter.Request{
		System:      strings.TrimSpace(tagMergePrompt),
		Prompt:      describeTag("A", a) + "\n" + describeTag("B", b),
		Temperature: &temp,
	}, &answer); err != nil {
		return false, fmt.Errorf("err tag merge answer: %w", err)
	}

	return answer.Same, nil
}

func describeTag(label string, t Tag) string {
	return fmt.Sprintf("%s: %s (%s)", label, t.Key, strings.Join(t.Terms, ", "))
}
