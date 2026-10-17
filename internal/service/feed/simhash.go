package feed

import (
	"hash/fnv"
	"regexp"
	"strings"
	"unicode"
)

const shingleSize = 3

var linkTarget = regexp.MustCompile(`\]\([^)]*\)`)

func simhash(text string) (uint64, bool) {
	words := strings.FieldsFunc(strings.ToLower(linkTarget.ReplaceAllString(text, "]")), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	if len(words) == 0 {
		return 0, false
	}

	var weights [64]int

	for i := range max(len(words)-shingleSize+1, 1) {
		h := fnv.New64a()
		h.Write([]byte(strings.Join(words[i:min(i+shingleSize, len(words))], " ")))
		sum := h.Sum64()

		for b := range weights {
			if sum&(1<<b) != 0 {
				weights[b]++
			} else {
				weights[b]--
			}
		}
	}

	var out uint64

	for b, w := range weights {
		if w > 0 {
			out |= 1 << b
		}
	}

	return out, true
}
