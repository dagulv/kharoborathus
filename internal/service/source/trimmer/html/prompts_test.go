package html

import (
	"reflect"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
)

func TestPrompts(t *testing.T) {
	if prompts.system == "" {
		t.Fatal("empty system prompt")
	}

	rt := reflect.TypeFor[Rule]()
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if prompts.fields[name] == "" {
			t.Errorf("no description for %q in prompts/rule.md", name)
		}
	}

	if len(prompts.fields) != rt.NumField() {
		t.Errorf("prompts/rule.md has %d sections for %d fields", len(prompts.fields), rt.NumField())
	}

	r := jsonschema.Reflector{Anonymous: true, DoNotReference: true, ExpandedStruct: true}
	if p, _ := r.Reflect(&Rule{}).Properties.Get("include"); p == nil || !strings.Contains(p.Description, "abstain") {
		t.Errorf("schema description not applied: %+v", p)
	}

	user := prompts.user(Skeleton{Pages: []string{"<div>\n", "<p>\n"}})
	if user != "=== page 1 ===\n<div>\n\n=== page 2 ===\n<p>\n\n" {
		t.Errorf("user prompt: %q", user)
	}
}
