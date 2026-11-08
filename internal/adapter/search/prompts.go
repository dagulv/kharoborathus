package discovery

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed prompts/*.md
var promptFiles embed.FS

var prompts = template.Must(template.New("").
	Funcs(template.FuncMap{"join": strings.Join}).
	ParseFS(promptFiles, "prompts/*.md"))

// System prompts take no data, so they stay identical across calls and DeepSeek can cache them.
var (
	discoverySystem    = mustRender("discovery_system_prompt")
	searchFilterSystem = mustRender("search_filter_system_prompt")
)

func render(name string, data any) (string, error) {
	var b strings.Builder

	if err := prompts.ExecuteTemplate(&b, name, data); err != nil {
		return "", fmt.Errorf("err render %s: %w", name, err)
	}

	return strings.TrimSpace(b.String()), nil
}

func mustRender(name string) string {
	s, err := render(name, nil)
	if err != nil {
		panic(err)
	}

	return s
}
