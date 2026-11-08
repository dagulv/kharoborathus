package discovery

import (
	"strings"
	"unicode/utf8"
)

const (
	maxTagKeys       = 300
	maxKeywords      = 5
	maxKeywordQuery  = 100
	maxKeywordEntity = 80
	maxKeywordAction = 30
)

type Keyword struct {
	Entity string `json:"entity"`
	Action string `json:"action"`
	Query  string `json:"query"`
}

func groundKeywords(keywords []Keyword, article string) []Keyword {
	text := strings.ToLower(article)
	seen := make(map[string]bool)
	out := make([]Keyword, 0, len(keywords))

	for _, k := range keywords {
		k.Entity = collapse(k.Entity)
		k.Action = tagKey(k.Action)
		k.Query = collapse(k.Query)

		entity := strings.ToLower(k.Entity)
		query := strings.ToLower(k.Query)

		switch {
		case k.Entity == "" || k.Action == "" || k.Query == "":
			continue
		case utf8.RuneCountInString(k.Entity) > maxKeywordEntity,
			utf8.RuneCountInString(k.Action) > maxKeywordAction,
			utf8.RuneCountInString(k.Query) > maxKeywordQuery:
			continue
		case !strings.Contains(text, entity) || !strings.Contains(query, entity):
			continue
		case seen[query]:
			continue
		}

		seen[query] = true
		out = append(out, k)

		if len(out) == maxKeywords {
			break
		}
	}

	return out
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func tagKey(s string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(collapse(s)))
}
