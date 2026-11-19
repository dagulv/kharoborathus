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
	l        *slog.Logger
}

// Scores by tag key, found in the question text.
func (f *fakeClassifier) Noul(_ context.Context, l *slog.Logger, state any, questions map[string]port.NoulQuestion) (map[string]float64, error) {
	f.l = l
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

	got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "Titel", strings.Repeat("å", 70_000), nil, tags)
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

	if got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "t", "a", nil, nil); err != nil || len(got) != 0 || len(c.requests) != 0 {
		t.Errorf("got %v, %v, requests %d", got, err, len(c.requests))
	}
}

func TestJevTagsError(t *testing.T) {
	c := &fakeClassifier{err: port.ErrUnavailable}

	if _, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "t", "a", nil, []domain.Tag{{Key: "k"}}); !errors.Is(err, port.ErrUnavailable) {
		t.Errorf("err %v", err)
	}
}

func asked(req map[string]port.NoulQuestion, key string) bool {
	for _, q := range req {
		if strings.Contains(q.Instructions, " "+key+" ") || strings.Contains(q.Instructions, " "+key+"?") {
			return true
		}
	}

	return false
}

func count(req map[string]port.NoulQuestion, key string) int {
	n := 0

	for _, q := range req {
		if strings.Contains(q.Instructions, " "+key+" ") || strings.Contains(q.Instructions, " "+key+"?") {
			n++
		}
	}

	return n
}

var twoStageCategories = []domain.Category{
	{Key: "energy", Terms: map[string]string{"sv": "energi"}},
	{Key: "finance"},
}

func twoStageTags() []domain.Tag {
	return []domain.Tag{
		{Key: "oil", Categories: []string{"energy"}},
		{Key: "dividend", Categories: []string{"finance"}},
		{Key: "batteries", Categories: []string{"energy", "finance"}},
		{Key: "misc"},
	}
}

func TestJevTagsTwoStage(t *testing.T) {
	c := &fakeClassifier{scores: map[string]float64{"energy": 0.5, "finance": 0.49, "oil": 0.9, "batteries": 0.9, "misc": 0.8, "dividend": 0.99}}

	got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "t", "a", twoStageCategories, twoStageTags())
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"oil", "batteries", "misc"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	if len(c.requests) != 2 || len(c.requests[0]) != 2 {
		t.Fatalf("requests %d", len(c.requests))
	}

	if !strings.Contains(c.requests[0]["category_0"].Instructions, "energy (energi)?") || strings.Contains(c.requests[0]["category_1"].Instructions, "(") {
		t.Errorf("questions %v", c.requests[0])
	}

	if len(c.requests[1]) != 3 || !asked(c.requests[1], "oil") || !asked(c.requests[1], "batteries") || !asked(c.requests[1], "misc") || asked(c.requests[1], "dividend") {
		t.Errorf("stage 2 %v", c.requests[1])
	}
}

func TestJevTagsNoCategories(t *testing.T) {
	c := &fakeClassifier{scores: map[string]float64{"oil": 0.9}}

	got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "t", "a", nil, twoStageTags())
	if err != nil {
		t.Fatal(err)
	}

	if len(c.requests) != 1 || len(c.requests[0]) != 4 || !slices.Equal(got, []string{"oil"}) {
		t.Errorf("got %v, requests %v", got, c.requests)
	}
}

func TestJevTagsMissingCategoryScore(t *testing.T) {
	c := &fakeClassifier{scores: map[string]float64{"oil": 0.9, "misc": 0.9}}
	tags := []domain.Tag{twoStageTags()[0], twoStageTags()[3]}

	got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "t", "a", twoStageCategories[:1], tags)
	if err != nil {
		t.Fatal(err)
	}

	if len(c.requests) != 2 || asked(c.requests[1], "oil") || !asked(c.requests[1], "misc") || !slices.Equal(got, []string{"misc"}) {
		t.Errorf("got %v, requests %v", got, c.requests)
	}
}

func TestJevTagsTagInTwoCategories(t *testing.T) {
	c := &fakeClassifier{scores: map[string]float64{"energy": 0.9, "finance": 0.9, "batteries": 0.9}}

	got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "t", "a", twoStageCategories, twoStageTags())
	if err != nil {
		t.Fatal(err)
	}

	if n := count(c.requests[1], "batteries"); n != 1 || !slices.Equal(got, []string{"batteries"}) {
		t.Errorf("asked %d times, got %v", n, got)
	}
}

func TestJevTagsCategoryError(t *testing.T) {
	c := &fakeClassifier{err: port.ErrUnavailable}

	if _, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "t", "a", twoStageCategories, twoStageTags()); !errors.Is(err, port.ErrUnavailable) || len(c.requests) != 1 {
		t.Errorf("err %v, requests %d", err, len(c.requests))
	}
}

func TestJevTagsProposed(t *testing.T) {
	c := &fakeClassifier{scores: map[string]float64{"energy": 0.9, "wind": 0.8}}
	tags := []domain.Tag{{Key: "wind", Status: domain.StatusProposed, Categories: []string{"energy"}}}

	got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "t", "a", twoStageCategories[:1], tags)
	if err != nil || !slices.Equal(got, []string{"wind"}) {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestExcerpt(t *testing.T) {
	md := "# Rubrik\n\n![bild](https://a.example/i.png)\n\nVolvo **köper** [Northvolt](https://a.example/n).\n\n> Citat\n\n- punkt"

	if got, want := excerpt(md), "Rubrik Volvo köper Northvolt. Citat punkt"; got != want {
		t.Errorf("got %q", got)
	}

	if got, want := excerpt(strings.Repeat("å", 278)+" öl är"), strings.Repeat("å", 278); got != want {
		t.Errorf("runes %d", len([]rune(got)))
	}

	if got, want := excerpt(strings.Repeat("å", 277)+" öl är"), strings.Repeat("å", 277)+" öl"; got != want {
		t.Errorf("runes %d", len([]rune(got)))
	}
}

func TestJevTagsStaleCategory(t *testing.T) {
	c := &fakeClassifier{scores: map[string]float64{"energy": 0.9, "ghost": 0.9}}
	tags := []domain.Tag{{Key: "ghost", Categories: []string{"removed"}}}

	got, err := jevTags(context.Background(), c, slog.New(slog.DiscardHandler), "sv", "t", "a", twoStageCategories[:1], tags)
	if err != nil || !slices.Equal(got, []string{"ghost"}) {
		t.Errorf("got %v, %v", got, err)
	}
}
