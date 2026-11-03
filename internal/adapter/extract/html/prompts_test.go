package html

import (
	"reflect"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
)

func TestPrompts(t *testing.T) {
	if prompts.content == "" {
		t.Fatal("empty content system prompt")
	}

	for file, c := range map[string]struct {
		rt       reflect.Type
		sections map[string]string
	}{
		"content_rule.md": {reflect.TypeFor[ContentRule](), prompts.contentFields},
		"field_rule.md":   {reflect.TypeFor[FieldRule](), prompts.fieldFields},
	} {
		for i := range c.rt.NumField() {
			name, _, _ := strings.Cut(c.rt.Field(i).Tag.Get("json"), ",")
			if c.sections[name] == "" {
				t.Errorf("no description for %q in prompts/%s", name, file)
			}
		}

		if len(c.sections) != c.rt.NumField() {
			t.Errorf("prompts/%s has %d sections for %d fields", file, len(c.sections), c.rt.NumField())
		}
	}

	for _, f := range []field{fieldTitle, fieldAuthors, fieldPostedAt} {
		if prompts.instructions[string(f)] == "" {
			t.Errorf("no instruction for %q in prompts/fields.md", f)
		}
	}

	r := jsonschema.Reflector{Anonymous: true, DoNotReference: true, ExpandedStruct: true}
	if p, _ := r.Reflect(&ContentRule{}).Properties.Get("include"); p == nil || !strings.Contains(p.Description, "abstain") {
		t.Errorf("schema description not applied: %+v", p)
	}

	user := prompts.user(Skeleton{Pages: []string{"<div>\n", "<p>\n"}})
	if user != "=== page 1 ===\n<div>\n\n=== page 2 ===\n<p>\n\n" {
		t.Errorf("user prompt: %q", user)
	}
}
