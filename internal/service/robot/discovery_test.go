package robot

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

type fakeLLM struct {
	answer discoveryAnswer
	req    adapter.Request
}

func (f *fakeLLM) Schedule() schedule.Schedule { return schedule.Always }

func (f *fakeLLM) Prompt(context.Context, adapter.Request) (string, error) { return "", nil }

func (f *fakeLLM) Unmarshal(_ context.Context, req adapter.Request, out any) error {
	f.req = req

	b, err := json.Marshal(f.answer)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, out)
}

const article = `# Volvo investerar i Göteborg

Volvo Cars investerar 10 miljarder kronor i en ny batterifabrik. Samtidigt tecknar Ericsson ett avtal med Telia om 5G.
Northvolt, Saab, Scania och ABB nämns också.`

func TestKeywords(t *testing.T) {
	llm := &fakeLLM{answer: discoveryAnswer{Keywords: []Keyword{
		{Entity: " Volvo  Cars ", Action: "Investment", Query: "Volvo Cars investerar  batterifabrik"},
		{Entity: "Ericsson", Action: "contract", Query: "Ericsson avtal Telia 5G"},
		{Entity: "Ericsson", Action: "contract", Query: "ericsson avtal telia 5g"},
		{Entity: "Nokia", Action: "contract", Query: "Nokia avtal Telia"},
		{Entity: "Telia", Action: "contract", Query: "avtal 5G"},
		{Entity: "Northvolt", Action: "expansion", Query: "Northvolt expansion"},
		{Entity: "Saab", Action: "order", Query: "Saab order"},
		{Entity: "Scania", Action: "order", Query: "Scania order"},
		{Entity: "ABB", Action: "order", Query: "ABB order"},
		{Entity: "", Action: "order", Query: "order"},
	}}}

	keywords, err := NewDiscoverer(llm).Keywords(context.Background(), article)
	if err != nil {
		t.Fatal(err)
	}

	want := []Keyword{
		{Entity: "Volvo Cars", Action: "investment", Query: "Volvo Cars investerar batterifabrik"},
		{Entity: "Ericsson", Action: "contract", Query: "Ericsson avtal Telia 5G"},
		{Entity: "Northvolt", Action: "expansion", Query: "Northvolt expansion"},
		{Entity: "Saab", Action: "order", Query: "Saab order"},
		{Entity: "Scania", Action: "order", Query: "Scania order"},
	}

	if len(keywords) != len(want) {
		t.Fatalf("got %d keywords: %+v", len(keywords), keywords)
	}

	for i := range want {
		if keywords[i] != want[i] {
			t.Errorf("keyword %d: got %+v, want %+v", i, keywords[i], want[i])
		}
	}

	if llm.req.Prompt != article || llm.req.System == "" {
		t.Error("article and prompt not sent to the model")
	}
}

func TestKeywordsNone(t *testing.T) {
	keywords, err := NewDiscoverer(&fakeLLM{}).Keywords(context.Background(), article)
	if err != nil || len(keywords) != 0 {
		t.Fatalf("got %v, %v", keywords, err)
	}
}
