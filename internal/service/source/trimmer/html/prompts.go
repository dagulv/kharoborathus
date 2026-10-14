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
	system      string
	page        *template.Template
	fields      map[string]string
	linksSystem string
	linkFields  map[string]string
}

func loadPrompts() promptSet {
	return promptSet{
		system: strings.TrimSpace(readPrompt("system.md")),
		page: template.Must(template.New("user.md").
			Funcs(template.FuncMap{"add": func(a, b int) int { return a + b }}).
			Parse(readPrompt("user.md"))),
		fields:      sections(readPrompt("rule.md")),
		linksSystem: strings.TrimSpace(readPrompt("links_system.md")),
		linkFields:  sections(readPrompt("link_rule.md")),
	}
}

func (p promptSet) user(s Skeleton) string {
	var b strings.Builder

	if err := p.page.Execute(&b, s); err != nil {
		panic(fmt.Sprintf("trimmer: prompts/user.md: %v", err))
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
