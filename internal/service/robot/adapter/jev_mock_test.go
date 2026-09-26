package adapter

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestJevMock(t *testing.T) {
	m := NewJevMock(slog.New(slog.NewTextHandler(io.Discard, nil)))

	state := map[string]string{"title": "Volvo investerar", "article": "Volvo Cars investerar 10 miljarder. Marknaden är bearish."}

	got, err := m.Noul(context.Background(), state, map[string]NoulQuestion{
		"investment": {Instructions: "Is this news article about investment (investment, investering)?"},
		"bearish":    {Instructions: "Is this news article about bearish?"},
		"sport":      {Instructions: "Is this news article about sport (sport, idrott)?"},
		"template":   {Instructions: "Is this news article about it?"},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]float64{"investment": mockYes, "bearish": mockYes, "sport": mockNo, "template": mockNo}

	for id, p := range want {
		if got[id] != p {
			t.Errorf("%s: got %v, want %v", id, got[id], p)
		}
	}

	if got, _ := m.Noul(context.Background(), "Ett avtal om investeringar.", map[string]NoulQuestion{"a": {Instructions: "Is this news article about investment (investering)?"}}); got["a"] != mockYes {
		t.Errorf("string state: %v", got)
	}
}
