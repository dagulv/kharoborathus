package robot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

type mergeLLM struct {
	*fakeLLM
	same bool
	err  error
}

func (f *mergeLLM) Unmarshal(_ context.Context, req adapter.Request, out any) error {
	f.fakeLLM.req = req

	if f.err != nil {
		return f.err
	}

	b, err := json.Marshal(mergeAnswer{Same: f.same})
	if err != nil {
		return err
	}

	return json.Unmarshal(b, out)
}

func TestSameTag(t *testing.T) {
	llm := &mergeLLM{fakeLLM: &fakeLLM{}, same: true}

	same, err := NewTagMerger(llm).Same(context.Background(),
		Tag{Key: "investments", Terms: []string{"investments", "investeringar"}},
		Tag{Key: "investment", Terms: []string{"investering", "investment"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !same {
		t.Error("want same")
	}

	if p := llm.req.Prompt; !strings.Contains(p, "A: investments (investments, investeringar)") || !strings.Contains(p, "B: investment (investering, investment)") {
		t.Errorf("prompt %q", p)
	}
}

func TestSameTagUnavailable(t *testing.T) {
	llm := &mergeLLM{fakeLLM: &fakeLLM{}, err: adapter.ErrUnavailable}

	if _, err := NewTagMerger(llm).Same(context.Background(), Tag{Key: "a"}, Tag{Key: "b"}); !errors.Is(err, adapter.ErrUnavailable) {
		t.Errorf("err %v", err)
	}
}
