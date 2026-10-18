package robot

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

type scoreClassifier struct {
	requests []map[string]adapter.NoulQuestion
	state    any
	scores   map[string]float64
	err      error
}

func (f *scoreClassifier) Noul(_ context.Context, _ *slog.Logger, state any, questions map[string]adapter.NoulQuestion) (map[string]float64, error) {
	f.requests = append(f.requests, questions)
	f.state = state

	if f.err != nil {
		return nil, f.err
	}

	out := make(map[string]float64, len(questions))

	for id, q := range questions {
		out[id] = f.scores[q.Instructions]
	}

	return out, nil
}

var candidateTag = Tag{Key: "investments", Terms: []string{"investments", "investeringar"}}

func TestDuplicateTag(t *testing.T) {
	c := &scoreClassifier{scores: map[string]float64{
		"Does this tag name the same concept as capital_raise (capital raise, nyemission)?": 0.85,
		"Does this tag name the same concept as investment (investering, investment)?":      0.95,
		"Does this tag name the same concept as acquisition (acquisition, förvärv)?":        0.1,
	}}

	key, err := NewTagMerger(c).Duplicate(context.Background(), slog.New(slog.DiscardHandler), candidateTag, []Tag{
		{Key: "acquisition", Terms: []string{"acquisition", "förvärv"}},
		{Key: "capital_raise", Terms: []string{"capital raise", "nyemission"}},
		{Key: "investment", Terms: []string{"investering", "investment"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if key != "investment" {
		t.Errorf("key %q", key)
	}

	if len(c.requests) != 1 || len(c.requests[0]) != 3 {
		t.Fatalf("requests %+v", c.requests)
	}

	for id, q := range c.requests[0] {
		if q.Criteria == nil || q.Criteria.True == "" || q.Criteria.False == "" {
			t.Errorf("question %s has no criteria", id)
		}
	}

	state, ok := c.state.(map[string]any)
	if !ok || state["key"] != "investments" {
		t.Errorf("state %+v", c.state)
	}
}

func TestDuplicateTagBelowThreshold(t *testing.T) {
	c := &scoreClassifier{scores: map[string]float64{
		"Does this tag name the same concept as investment (investering, investment)?": 0.7,
	}}

	key, err := NewTagMerger(c).Duplicate(context.Background(), slog.New(slog.DiscardHandler), candidateTag, []Tag{
		{Key: "investment", Terms: []string{"investering", "investment"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if key != "" {
		t.Errorf("key %q", key)
	}
}

func TestDuplicateTagNoExisting(t *testing.T) {
	c := &scoreClassifier{}

	key, err := NewTagMerger(c).Duplicate(context.Background(), slog.New(slog.DiscardHandler), candidateTag, nil)
	if err != nil {
		t.Fatal(err)
	}

	if key != "" || len(c.requests) != 0 {
		t.Errorf("key %q, requests %d", key, len(c.requests))
	}
}

func TestDuplicateTagUnavailable(t *testing.T) {
	c := &scoreClassifier{err: adapter.ErrUnavailable}

	if _, err := NewTagMerger(c).Duplicate(context.Background(), slog.New(slog.DiscardHandler), candidateTag, []Tag{{Key: "investment"}}); !errors.Is(err, adapter.ErrUnavailable) {
		t.Errorf("err %v", err)
	}
}
