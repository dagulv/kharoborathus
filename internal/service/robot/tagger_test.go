package robot

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

type fakeClassifier struct {
	requests []map[string]adapter.NoulQuestion
	state    any
	yes      map[string]bool
}

func (f *fakeClassifier) Noul(_ context.Context, state any, questions map[string]adapter.NoulQuestion) (map[string]float64, error) {
	f.requests = append(f.requests, questions)
	f.state = state

	out := make(map[string]float64, len(questions))

	for id, q := range questions {
		out[id] = 0.1

		if f.yes[q.Instructions] {
			out[id] = 0.95
		}
	}

	return out, nil
}

func TestTags(t *testing.T) {
	tags := []Tag{
		{Key: "investment", Terms: []string{"investment", "investering"}},
		{Key: "sport"},
	}

	for i := range 60 {
		tags = append(tags, Tag{Key: fmt.Sprintf("filler-%d", i)})
	}

	c := &fakeClassifier{yes: map[string]bool{
		"Is this news article about investment (investment, investering)?": true,
		"Is this news article about filler-59?":                            true,
	}}

	keys, err := NewTagger(c).Tags(context.Background(), "Volvo investerar", "Volvo Cars investerar.", tags)
	if err != nil {
		t.Fatal(err)
	}

	if len(keys) != 2 || keys[0] != "investment" || keys[1] != "filler-59" {
		t.Errorf("keys %v", keys)
	}

	if len(c.requests) != 2 || len(c.requests[0]) != tagsPerRequest || len(c.requests[1]) != 12 {
		t.Errorf("requests of %d and %d questions", len(c.requests[0]), len(c.requests[1]))
	}

	if q := c.requests[0]["tag_1"].Instructions; q != "Is this news article about sport?" {
		t.Errorf("question %q", q)
	}

	if s := c.state.(map[string]string); s["title"] != "Volvo investerar" || s["article"] != "Volvo Cars investerar." {
		t.Errorf("state %v", s)
	}
}
