package tagging

import (
	"strings"

	"github.com/dagulv/kharoborathus/internal/service/robot"
)

type routed struct {
	terms      []Term
	reactivate []string
	proposals  []Term
}

func route(v Vocabulary, lang string, generated []robot.GeneratedTag) (r routed) {
	seen := make(map[string]bool)

	for _, g := range generated {
		key := g.Key
		if canonical, ok := v.Aliases[key]; ok {
			key = canonical
		}

		if seen[key] {
			continue
		}

		seen[key] = true

		state, known := v.Tags[key]
		if !known {
			r.proposals = append(r.proposals, Term{Key: key, Lang: lang, Term: g.Term})
			continue
		}

		if !state.Enabled {
			continue
		}

		if state.Status == statusDormant {
			r.reactivate = append(r.reactivate, key)
		}

		r.terms = append(r.terms, termsFor(key, []Term{{Lang: lang, Term: g.Term}})...)
	}

	return r
}

// termsFor always leads with the English term derived from the key, the same rule search relies on.
func termsFor(key string, proposed []Term) []Term {
	terms := []Term{{Key: key, Lang: englishLang, Term: strings.ReplaceAll(key, "_", " ")}}
	seen := map[Term]bool{terms[0]: true}

	for _, p := range proposed {
		t := Term{Key: key, Lang: p.Lang, Term: p.Term}

		if t.Lang == "" || t.Lang == englishLang || t.Term == "" || seen[t] {
			continue
		}

		seen[t] = true
		terms = append(terms, t)
	}

	return terms
}
