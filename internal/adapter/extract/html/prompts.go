package html

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed prompts/*.md
var promptFiles embed.FS

var prompts = loadPrompts()

type promptSet struct {
	content       string
	page          *template.Template
	contentFields map[string]string
	field         *template.Template
	instructions  map[string]string
	fieldFields   map[string]string
	links         *template.Template
	linkFields    map[string]string
	pagesSystem   string
	pageFields    map[string]string
}

func loadPrompts() promptSet {
	return promptSet{
		content: strings.TrimSpace(readPrompt("content_system.md")),
		page: template.Must(template.New("user.md").
			Funcs(template.FuncMap{"add": func(a, b int) int { return a + b }}).
			Parse(readPrompt("user.md"))),
		contentFields: sections(readPrompt("content_rule.md")),
		field:         template.Must(template.New("field_system.md").Parse(readPrompt("field_system.md"))),
		instructions:  sections(readPrompt("fields.md")),
		fieldFields:   sections(readPrompt("field_rule.md")),
		links:         template.Must(template.New("links_system.md").Parse(readPrompt("links_system.md"))),
		linkFields:    sections(readPrompt("link_rule.md")),
		pagesSystem:   strings.TrimSpace(readPrompt("pages_system.md")),
		pageFields:    sections(readPrompt("page_rule.md")),
	}
}

func (p promptSet) user(s Skeleton) string {
	var b strings.Builder

	if err := p.page.Execute(&b, s); err != nil {
		panic(fmt.Sprintf("extract: prompts/user.md: %v", err))
	}

	return b.String()
}

func (p promptSet) fieldSystem(f field) string {
	var b strings.Builder

	if err := p.field.Execute(&b, p.instructions[string(f)]); err != nil {
		panic(fmt.Sprintf("extract: prompts/field_system.md: %v", err))
	}

	return strings.TrimSpace(b.String())
}

// A single capture has no headline changes to annotate, so its prompt drops the annotations.
func (p promptSet) linksSystem(pages int) string {
	var b strings.Builder

	if err := p.links.Execute(&b, struct{ Single bool }{pages == 1}); err != nil {
		panic(fmt.Sprintf("extract: prompts/links_system.md: %v", err))
	}

	return strings.TrimSpace(b.String())
}

func (p promptSet) candidates(cands []candidate) string {
	var b strings.Builder

	for i, c := range cands {
		fmt.Fprintf(&b, "[%d] <%s", i, c.tag)

		if len(c.classes) > 0 {
			fmt.Fprintf(&b, ` class="%s"`, strings.Join(c.classes, " "))
		}

		if c.rel != "" {
			fmt.Fprintf(&b, ` rel="%s"`, c.rel)
		}

		if c.ariaLabel != "" {
			fmt.Fprintf(&b, ` aria-label="%s"`, c.ariaLabel)
		}

		fmt.Fprintf(&b, "> %q -> %s\n", c.text, c.url)
	}

	return b.String()
}

func readPrompt(name string) string {
	b, err := promptFiles.ReadFile("prompts/" + name)
	if err != nil {
		panic(err)
	}

	return string(b)
}

func sections(md string) map[string]string {
	out := make(map[string]string)

	var name string
	var body strings.Builder

	flush := func() {
		if name != "" {
			out[name] = strings.TrimSpace(body.String())
		}

		body.Reset()
	}

	for line := range strings.Lines(md) {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			name = strings.TrimSpace(h)
			continue
		}

		body.WriteString(line)
	}

	flush()

	return out
}
