package trimmer

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"
)

var ErrNoRule = errors.New("trimmer: no rule could be generated")

type Trimmer[R any] interface {
	GenerateRuleset(ctx context.Context, l *slog.Logger, samples []Sample) (R, error)
	Trim(rule R, data []byte) (Extraction, error)
}

type Linker[R any] interface {
	GenerateRuleset(ctx context.Context, l *slog.Logger, samples []Sample, fetch Fetch) (R, error)
	Links(rule R, listing Sample) ([]string, error)
}

type Fetch func(ctx context.Context, url string) ([]byte, error)

type Sample struct {
	URL  string
	Data []byte
}

type Extraction struct {
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	Published time.Time `json:"published"`
	Content   string    `json:"content"`
	Text      string    `json:"text"`
	Failures  []string  `json:"failures"`
}

func (e Extraction) OK() bool {
	return len(e.Failures) == 0
}

func (e Extraction) Len() int {
	return utf8.RuneCountInString(e.Text)
}
