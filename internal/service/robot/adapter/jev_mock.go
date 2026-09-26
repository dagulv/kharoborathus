package adapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"unicode"
)

const (
	mockYes     = 0.95
	mockNo      = 0.05
	mockStemLen = 6
	mockMinWord = 3
)

var mockStopWords = map[string]bool{
	"is": true, "this": true, "news": true, "article": true, "about": true,
	"the": true, "a": true, "an": true, "it": true, "does": true,
}

var _ Classifier = jevMock{}

type jevMock struct {
	l *slog.Logger
}

func NewJevMock(l *slog.Logger) Classifier {
	return jevMock{l: l}
}

func (m jevMock) Noul(ctx context.Context, state any, questions map[string]NoulQuestion) (map[string]float64, error) {
	b, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}

	var text string
	if err := json.Unmarshal(b, &text); err != nil {
		text = string(b)
	}

	text = strings.ToLower(text)
	out := make(map[string]float64, len(questions))

	for id, q := range questions {
		out[id] = mockNo

		for _, stem := range mockWords(q.Instructions) {
			if strings.Contains(text, stem) {
				out[id] = mockYes
				break
			}
		}
	}

	m.l.DebugContext(ctx, "jev mock", "questions", len(questions))

	return out, nil
}

func mockWords(s string) []string {
	var words []string

	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if len([]rune(w)) >= mockMinWord && !mockStopWords[w] {
			words = append(words, string([]rune(w)[:min(len([]rune(w)), mockStemLen)]))
		}
	}

	return words
}
