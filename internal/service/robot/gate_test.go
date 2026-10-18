package robot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

var gateTags = []Tag{
	{Key: "acquisition", Terms: []string{"förvärv"}, Gate: true},
	{Key: "sweden"},
}

const (
	acquisitionQuestion = "Is this news article about acquisition (förvärv)?"
	swedenQuestion      = "Is this news article about sweden?"
)

func TestGatePasses(t *testing.T) {
	c := &fakeClassifier{yes: map[string]bool{
		strings.TrimSpace(articlePrompt): true,
		acquisitionQuestion:              true,
		swedenQuestion:                   true,
	}}

	v, err := NewTagger(c).Gate(context.Background(), slog.New(slog.DiscardHandler), "Volvo köper", "Volvo köper Northvolt.", gateTags)
	if err != nil {
		t.Fatal(err)
	}

	if !v.Passed || v.Article != 0.95 || !slices.Equal(v.Keys, []string{"acquisition", "sweden"}) {
		t.Errorf("verdict %+v", v)
	}

	if len(c.requests) != 1 || len(c.requests[0]) != 3 {
		t.Fatalf("requests %+v", c.requests)
	}

	if q := c.requests[0][articleID]; q.Criteria == nil || q.Criteria.True == "" || q.Criteria.False == "" {
		t.Errorf("article question %+v", q)
	}
}

func TestGateNeedsGateTag(t *testing.T) {
	c := &fakeClassifier{yes: map[string]bool{
		strings.TrimSpace(articlePrompt): true,
		swedenQuestion:                   true,
	}}

	v, err := NewTagger(c).Gate(context.Background(), slog.New(slog.DiscardHandler), "Sverige", "Om Sverige.", gateTags)
	if err != nil {
		t.Fatal(err)
	}

	if v.Passed || !slices.Equal(v.Keys, []string{"sweden"}) {
		t.Errorf("verdict %+v", v)
	}
}

func TestGateNeedsArticle(t *testing.T) {
	c := &fakeClassifier{yes: map[string]bool{acquisitionQuestion: true}}

	v, err := NewTagger(c).Gate(context.Background(), slog.New(slog.DiscardHandler), "Förvärv", "Lista över förvärv.", gateTags)
	if err != nil {
		t.Fatal(err)
	}

	if v.Passed || v.Article != 0.1 {
		t.Errorf("verdict %+v", v)
	}
}

func TestGateBatches(t *testing.T) {
	tags := slices.Clone(gateTags)

	for i := range 60 {
		tags = append(tags, Tag{Key: fmt.Sprintf("filler-%d", i)})
	}

	c := &fakeClassifier{}

	if _, err := NewTagger(c).Gate(context.Background(), slog.New(slog.DiscardHandler), "t", "a", tags); err != nil {
		t.Fatal(err)
	}

	if len(c.requests) != 2 || len(c.requests[0]) != tagsPerRequest+1 || len(c.requests[1]) != 12 {
		t.Fatalf("requests of %d and %d questions", len(c.requests[0]), len(c.requests[1]))
	}

	if _, ok := c.requests[1][articleID]; ok {
		t.Error("article question asked twice")
	}
}

func TestVerdictJSON(t *testing.T) {
	b, err := json.Marshal(Verdict{Article: 0.9, Tags: map[string]float64{"k": 0.5}, Keys: []string{"k"}, Passed: true})
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != `{"article":0.9,"tags":{"k":0.5}}` {
		t.Errorf("json %s", b)
	}
}

func TestQualityPasses(t *testing.T) {
	c := &fakeClassifier{yes: map[string]bool{strings.TrimSpace(articlePrompt): true}}

	passed, err := NewTagger(c).Quality(context.Background(), slog.New(slog.DiscardHandler), "Volvo köper", "Volvo köper Northvolt.")
	if err != nil {
		t.Fatal(err)
	}

	if !passed {
		t.Error("want passed")
	}

	if len(c.requests) != 1 || len(c.requests[0]) != 1 {
		t.Fatalf("requests %+v", c.requests)
	}

	if q := c.requests[0][articleID]; q.Criteria == nil || q.Criteria.True == "" || q.Criteria.False == "" {
		t.Errorf("article question %+v", q)
	}
}

func TestQualityRejects(t *testing.T) {
	c := &fakeClassifier{}

	passed, err := NewTagger(c).Quality(context.Background(), slog.New(slog.DiscardHandler), "Kategori", "Lista över artiklar.")
	if err != nil {
		t.Fatal(err)
	}

	if passed {
		t.Error("want rejected")
	}
}

func TestQualityUnavailable(t *testing.T) {
	_, err := NewTagger(errClassifier{}).Quality(context.Background(), slog.New(slog.DiscardHandler), "Volvo köper", "Volvo köper Northvolt.")
	if !errors.Is(err, adapter.ErrUnavailable) {
		t.Errorf("err %v", err)
	}
}

type errClassifier struct{}

func (errClassifier) Noul(context.Context, *slog.Logger, any, map[string]adapter.NoulQuestion) (map[string]float64, error) {
	return nil, adapter.ErrUnavailable
}
