package adapter

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()

	ts, err := time.Parse(time.RFC3339, s)

	if err != nil {
		t.Fatal(err)
	}

	return ts
}

func TestOffPeak(t *testing.T) {
	cases := []struct {
		name  string
		at    string
		avail bool
	}{
		{"before first peak window", "2026-09-21T00:59:59Z", true},
		{"first peak window", "2026-09-21T01:00:00Z", false},
		{"first peak window end", "2026-09-21T04:00:00Z", true},
		{"gap between windows", "2026-09-21T05:30:00Z", true},
		{"second peak window", "2026-09-21T09:59:59Z", false},
		{"second peak window end", "2026-09-21T10:00:00Z", true},
		{"after peak", "2026-09-21T23:00:00Z", true},
		{"saturday", "2026-09-19T02:00:00Z", true},
		{"sunday", "2026-09-20T07:00:00Z", true},
		{"national day holiday", "2026-10-01T02:00:00Z", true},
		{"last day of national day holiday", "2026-10-07T09:00:00Z", true},
		{"first workday after national day", "2026-10-08T09:00:00Z", false},
		{"spring festival", "2026-02-18T07:00:00Z", true},
		{"day before spring festival", "2026-02-13T07:00:00Z", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OffPeak.Active(mustParse(t, c.at)); got != c.avail {
				t.Errorf("Active(%s) = %t, want %t", c.at, got, c.avail)
			}
		})
	}
}

func TestNextOffPeak(t *testing.T) {
	cases := []struct {
		name string
		at   string
		want string
	}{
		{"off peak is now", "2026-09-21T23:00:00Z", "2026-09-21T23:00:00Z"},
		{"first peak window", "2026-09-21T01:30:00Z", "2026-09-21T04:00:00Z"},
		{"second peak window", "2026-09-21T06:00:00Z", "2026-09-21T10:00:00Z"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := OffPeak.Next(mustParse(t, c.at))

			if !got.Equal(mustParse(t, c.want)) {
				t.Errorf("Next(%s) = %s, want %s", c.at, got.Format(time.RFC3339), c.want)
			}
		})
	}
}

func TestSchemaOf(t *testing.T) {
	type classification struct {
		Category string   `json:"category" jsonschema:"enum=news,enum=opinion"`
		Tags     []string `json:"tags"`
	}

	schema, err := schemaOf(&classification{})

	if err != nil {
		t.Fatal(err)
	}

	props, ok := schema.Properties.(interface{ Len() int })

	if !ok || props.Len() != 2 {
		t.Errorf("schema properties = %v, want 2 properties", schema.Properties)
	}

	if len(schema.Required) != 2 {
		t.Errorf("schema required = %v, want both fields", schema.Required)
	}

	if _, err = schemaOf(classification{}); err == nil {
		t.Error("schemaOf(non pointer) = nil error, want error")
	}
}

func TestParamsDisableThinking(t *testing.T) {
	params, err := deepSeek{}.params(Request{Prompt: "hej"})
	if err != nil {
		t.Fatal(err)
	}

	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(b), `"thinking":{"type":"disabled"}`) {
		t.Errorf("thinking not disabled: %s", b)
	}
}
