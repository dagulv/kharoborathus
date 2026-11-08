package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

type fakeLLM struct {
	answer any
	err    error
	req    port.Request
}

func (f *fakeLLM) Schedule() schedule.Schedule { return schedule.Always }

func (f *fakeLLM) Prompt(context.Context, *slog.Logger, port.Request) (string, error) {
	return "", nil
}

func (f *fakeLLM) Unmarshal(_ context.Context, _ *slog.Logger, req port.Request, out any) error {
	f.req = req

	if f.err != nil {
		return f.err
	}

	b, err := json.Marshal(f.answer)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, out)
}

const article = `# Volvo investerar i Göteborg

Volvo Cars investerar 10 miljarder kronor i en ny batterifabrik. Samtidigt tecknar Ericsson ett avtal med Telia om 5G.
Northvolt, Saab, Scania och ABB nämns också.`

func TestGroundKeywords(t *testing.T) {
	got := groundKeywords([]Keyword{
		{Entity: " Volvo  Cars ", Action: "Investment", Query: "Volvo Cars investerar  batterifabrik"},
		{Entity: "Ericsson", Action: "contract", Query: "Ericsson avtal Telia 5G"},
		{Entity: "Ericsson", Action: "contract", Query: "ericsson avtal telia 5g"},
		{Entity: "Nokia", Action: "contract", Query: "Nokia avtal Telia"},
		{Entity: "Telia", Action: "contract", Query: "avtal 5G"},
		{Entity: "Northvolt", Action: "New  Factory", Query: "Northvolt expansion"},
		{Entity: "Saab", Action: "order", Query: "Saab order"},
		{Entity: "Scania", Action: "order", Query: "Scania order"},
		{Entity: "ABB", Action: "order", Query: "ABB order"},
		{Entity: "", Action: "order", Query: "order"},
	}, article)

	want := []Keyword{
		{Entity: "Volvo Cars", Action: "investment", Query: "Volvo Cars investerar batterifabrik"},
		{Entity: "Ericsson", Action: "contract", Query: "Ericsson avtal Telia 5G"},
		{Entity: "Northvolt", Action: "new_factory", Query: "Northvolt expansion"},
		{Entity: "Saab", Action: "order", Query: "Saab order"},
		{Entity: "Scania", Action: "order", Query: "Scania order"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestEventsUserPrompt(t *testing.T) {
	keys := make([]string, 400)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
	}

	p, err := EventsUserPrompt(keys)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(p, "Quality tag keys: k0, k1,") || !strings.Contains(p, "k299") || strings.Contains(p, "k300") {
		t.Errorf("prompt %q", p)
	}
}

func TestEventsUserPromptNone(t *testing.T) {
	p, err := EventsUserPrompt(nil)
	if err != nil || !strings.Contains(p, "Quality tag keys: none") {
		t.Errorf("prompt %q, %v", p, err)
	}
}

func TestEventsSystemPrompt(t *testing.T) {
	if !strings.Contains(EventsSystemPrompt, "events:") || strings.Contains(EventsSystemPrompt, "keywords:") {
		t.Errorf("prompt %q", EventsSystemPrompt)
	}
}
