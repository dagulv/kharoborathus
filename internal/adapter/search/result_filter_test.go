package discovery

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/port"
)

var searchResults = []SearchResult{
	{URL: "https://a.example/1", Title: "Volvo köper Northvolt", Hostname: "a.example"},
	{URL: "https://b.example/2", Title: "Volvo-aktien", Hostname: "b.example"},
	{URL: "https://c.example/3", Title: "Volvo förvärvar Northvolt", Description: "Affären", ExtraSnippets: []string{"Mer", "Mest"}, PageAge: "2026-10-16T08:00:00", Hostname: "c.example"},
}

// Scores a result by its title.
type fakeClassifier struct {
	scores map[string]float64
	err    error

	mu     sync.Mutex
	states []map[string]string
}

func (f *fakeClassifier) Noul(_ context.Context, _ *slog.Logger, state any, _ map[string]port.NoulQuestion) (map[string]float64, error) {
	s := state.(map[string]string)

	f.mu.Lock()
	f.states = append(f.states, s)
	f.mu.Unlock()

	if f.err != nil {
		return nil, f.err
	}

	return map[string]float64{"result": f.scores[s["title"]]}, nil
}

func (f *fakeClassifier) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.states)
}

// Keeps results 0 and 2.
func keepNorthvolt() *fakeClassifier {
	return &fakeClassifier{scores: map[string]float64{"Volvo köper Northvolt": 0.9, "Volvo-aktien": 0.79, "Volvo förvärvar Northvolt": 0.8}}
}

func TestKeep(t *testing.T) {
	c := keepNorthvolt()

	got, err := NewResultFilter(c).Keep(context.Background(), slog.New(slog.DiscardHandler), "Volvo", "acquisition", searchResults)
	if err != nil {
		t.Fatal(err)
	}

	if want := []int{0, 2}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	if c.calls() != len(searchResults) {
		t.Fatalf("calls %d", c.calls())
	}

	i := slices.IndexFunc(c.states, func(s map[string]string) bool { return s["hostname"] == "c.example" })
	want := map[string]string{
		"entity":         "Volvo",
		"action":         "acquisition",
		"title":          "Volvo förvärvar Northvolt",
		"description":    "Affären",
		"extra_snippets": "Mer\nMest",
		"page_age":       "2026-10-16T08:00:00",
		"hostname":       "c.example",
	}

	if i < 0 || len(c.states[i]) != len(want) {
		t.Fatalf("states %v", c.states)
	}

	for k, v := range want {
		if c.states[i][k] != v {
			t.Errorf("%s: got %q, want %q", k, c.states[i][k], v)
		}
	}
}

func TestKeepNoResults(t *testing.T) {
	c := &fakeClassifier{}

	got, err := NewResultFilter(c).Keep(context.Background(), slog.New(slog.DiscardHandler), "Volvo", "acquisition", nil)
	if err != nil || len(got) != 0 || c.calls() != 0 {
		t.Fatalf("got %v, %v, calls %d", got, err, c.calls())
	}
}

func TestKeepError(t *testing.T) {
	c := &fakeClassifier{err: port.ErrUnavailable}

	if got, err := NewResultFilter(c).Keep(context.Background(), slog.New(slog.DiscardHandler), "Volvo", "acquisition", searchResults); err == nil || got != nil {
		t.Errorf("got %v, %v", got, err)
	}
}
