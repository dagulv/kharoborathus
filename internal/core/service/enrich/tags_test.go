package enrich

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
)

type fakeClassifier struct {
	scores   map[string]float64
	err      error
	requests []map[string]port.NoulQuestion
	state    map[string]string
}

// Scores by tag key, found in the question text.
func (f *fakeClassifier) Noul(_ context.Context, _ *slog.Logger, state any, questions map[string]port.NoulQuestion) (map[string]float64, error) {
	f.requests = append(f.requests, questions)
	f.state = state.(map[string]string)

	if f.err != nil {
		return nil, f.err
	}

	out := make(map[string]float64, len(questions))

	for id, q := range questions {
		for key, score := range f.scores {
			if strings.Contains(q.Instructions, " "+key+" ") || strings.Contains(q.Instructions, " "+key+"?") {
				out[id] = score
			}
		}
	}

	return out, nil
}

func TestJevTags(t *testing.T) {
	tags := make([]domain.Tag, 120)
	for i := range tags {
		tags[i] = domain.Tag{Key: fmt.Sprintf("k%03d", i)}
	}

	tags[0].Terms = []string{"förvärv", "uppköp"}

	c := &fakeClassifier{scores: map[string]float64{"k000": 0.8, "k050": 0.95, "k119": 0.79}}

	got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "Titel", strings.Repeat("å", 70_000), tags)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"k000", "k050"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	if len(c.requests) != 3 || len(c.requests[0]) != 50 || len(c.requests[2]) != 20 {
		t.Errorf("request sizes %d", len(c.requests))
	}

	if !strings.Contains(c.requests[0]["tag_0"].Instructions, "(förvärv, uppköp)") {
		t.Errorf("question %q", c.requests[0]["tag_0"].Instructions)
	}

	if c.state["title"] != "Titel" || len([]rune(c.state["article"])) != 60_000 {
		t.Errorf("state title %q, runes %d", c.state["title"], len([]rune(c.state["article"])))
	}
}

func TestJevTagsNone(t *testing.T) {
	c := &fakeClassifier{}

	if got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "t", "a", nil); err != nil || len(got) != 0 || len(c.requests) != 0 {
		t.Errorf("got %v, %v, requests %d", got, err, len(c.requests))
	}
}

func TestJevTagsError(t *testing.T) {
	c := &fakeClassifier{err: port.ErrUnavailable}

	if _, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "t", "a", []domain.Tag{{Key: "k"}}); !errors.Is(err, port.ErrUnavailable) {
		t.Errorf("err %v", err)
	}
}

func TestExcerpt(t *testing.T) {
	md := "# Rubrik\n\n![bild](https://a.example/i.png)\n\nVolvo **köper** [Northvolt](https://a.example/n).\n\n> Citat\n\n- punkt"

	if got, want := excerpt(md), "Rubrik Volvo köper Northvolt. Citat punkt"; got != want {
		t.Errorf("got %q", got)
	}

	if got := excerpt(strings.Repeat("å ", 300)); len([]rune(got)) != 280 {
		t.Errorf("runes %d", len([]rune(got)))
	}
}
