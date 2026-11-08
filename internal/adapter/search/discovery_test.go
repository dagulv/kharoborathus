package discovery

import (
	"slices"
	"testing"
)

func TestEventItems(t *testing.T) {
	got := eventItems("p", []Keyword{
		{Entity: "Volvo", Action: "takeover", Query: "Volvo köper Northvolt"},
		{Entity: "Saab", Action: "order", Query: "Saab order"},
	}, map[string]string{"takeover": "acquisition"})

	want := []DiscoverItem{
		{PostID: "p", Entity: "Volvo", Action: "acquisition", Query: "Volvo köper Northvolt"},
		{PostID: "p", Entity: "Saab", Action: "order", Query: "Saab order"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("got %+v", got)
	}
}
