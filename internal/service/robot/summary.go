package robot

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	maxSummaryInput = 20_000
	maxSummaryLen   = 600
)

//go:embed prompts/summary.md
var summaryPrompt string

var ErrEmptySummary = errors.New("robot: empty summary")

type Summarizer struct {
	llm adapter.LLM
}

func NewSummarizer(llm adapter.LLM) *Summarizer {
	return &Summarizer{llm: llm}
}

func (s *Summarizer) Summarize(ctx context.Context, l *slog.Logger, markdown string) (string, error) {
	if r := []rune(markdown); len(r) > maxSummaryInput {
		markdown = string(r[:maxSummaryInput])
	}

	temp := 0.3

	summary, err := s.llm.Prompt(ctx, l, adapter.Request{
		System:      strings.TrimSpace(summaryPrompt),
		Prompt:      markdown,
		Temperature: &temp,
	})
	if err != nil {
		return "", err
	}

	summary = strings.Join(strings.Fields(summary), " ")

	if summary == "" {
		return "", ErrEmptySummary
	}

	if utf8.RuneCountInString(summary) > maxSummaryLen {
		summary = string([]rune(summary)[:maxSummaryLen]) + "…"
	}

	return summary, nil
}
