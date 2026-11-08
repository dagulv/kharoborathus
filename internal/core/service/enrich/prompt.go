package enrich

import (
	"embed"
	"fmt"
	"strings"
	"text/template"

	discovery "github.com/dagulv/kharoborathus/internal/adapter/search"
)

const (
	maxTagKeys    = 300
	maxArticleLLM = 20_000
)

//go:embed prompts/*.md
var promptFiles embed.FS

var prompts = template.Must(template.New("").
	Funcs(template.FuncMap{"join": strings.Join}).
	ParseFS(promptFiles, "prompts/*.md"))

// Static, so DeepSeek can cache it across calls.
var systemPrompt = mustRender("enrich_system_prompt", map[string]any{"Events": discovery.EventsSystemPrompt})

func userPrompt(lang string, keys, qualityKeys []string, markdown string) (string, error) {
	events, err := discovery.EventsUserPrompt(qualityKeys)
	if err != nil {
		return "", err
	}

	if r := []rune(markdown); len(r) > maxArticleLLM {
		markdown = string(r[:maxArticleLLM])
	}

	return render("enrich_user_prompt", map[string]any{
		"Lang":    lang,
		"Keys":    keys[:min(len(keys), maxTagKeys)],
		"Events":  events,
		"Article": markdown,
	})
}

func render(name string, data any) (string, error) {
	var b strings.Builder

	if err := prompts.ExecuteTemplate(&b, name, data); err != nil {
		return "", fmt.Errorf("err render %s: %w", name, err)
	}

	return strings.TrimSpace(b.String()), nil
}

func mustRender(name string, data any) string {
	s, err := render(name, data)
	if err != nil {
		panic(err)
	}

	return s
}
