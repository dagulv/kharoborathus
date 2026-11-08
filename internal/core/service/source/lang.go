package source

import "strings"

// tag_terms and the posts text search only recognise bare codes like sv and en.
func normalizeLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}

	return lang
}
