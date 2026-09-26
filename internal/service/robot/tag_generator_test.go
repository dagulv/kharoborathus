package robot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

func TestGenerateTags(t *testing.T) {
	llm := &fakeLLM{}
	answer := tagGenerateAnswer{Tags: []GeneratedTag{
		{Key: "Investment", Term: " Investering "},
		{Key: "share buyback", Term: "återköp"},
		{Key: "price-rise", Term: "uppgång"},
		{Key: "investment", Term: "investeringar"},
		{Key: "bearish", Term: ""},
		{Key: "_bullish", Term: "uppåt"},
		{Key: "price_fall", Term: "fall"},
		{Key: "layoffs", Term: "varsel"},
		{Key: "dividend", Term: "utdelning"},
		{Key: "ipo", Term: "börsnotering"},
	}}

	tags, err := NewTagGenerator(&tagLLM{fakeLLM: llm, answer: answer}).Generate(context.Background(), "sv", "Volvo investerar.", []string{"investment", "bullish"})
	if err != nil {
		t.Fatal(err)
	}

	want := []GeneratedTag{
		{Key: "investment", Term: "investering"},
		{Key: "share_buyback", Term: "återköp"},
		{Key: "price_rise", Term: "uppgång"},
		{Key: "price_fall", Term: "fall"},
		{Key: "layoffs", Term: "varsel"},
	}

	if len(tags) != len(want) {
		t.Fatalf("got %+v", tags)
	}

	for i := range want {
		if tags[i] != want[i] {
			t.Errorf("tag %d: got %+v, want %+v", i, tags[i], want[i])
		}
	}

	if p := llm.req.Prompt; !strings.Contains(p, "Language: sv") || !strings.Contains(p, "Existing tag keys: investment, bullish") || !strings.Contains(p, "Volvo investerar.") {
		t.Errorf("prompt %q", p)
	}
}

type tagLLM struct {
	*fakeLLM
	answer tagGenerateAnswer
}

func (f *tagLLM) Unmarshal(ctx context.Context, req adapter.Request, out any) error {
	f.fakeLLM.req = req

	b, err := json.Marshal(f.answer)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, out)
}
