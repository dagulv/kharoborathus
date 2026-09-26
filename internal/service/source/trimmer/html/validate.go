package html

import (
	"fmt"
	"unicode/utf8"

	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
)

const (
	minHeldOut = 5
	minRecall  = 0.95
)

func validate(c *compiledRule, heldOut []trimmer.Sample) (removed float64, err error) {
	if len(heldOut) < minHeldOut {
		return 0, fmt.Errorf("trimmer: %d held out pages, at least %d needed", len(heldOut), minHeldOut)
	}

	var ruleLen, genericLen int

	for _, p := range heldOut {
		e := trimRule(c, p.Data)
		g := trimGeneric(p.Data)

		if !e.OK() {
			return 0, fmt.Errorf("trimmer: %s: %v", p.URL, e.Failures)
		}

		if r := recall(e, g); r < minRecall {
			return 0, fmt.Errorf("trimmer: %s: kept %.0f%% of the paragraphs", p.URL, r*100)
		}

		ruleLen += utf8.RuneCountInString(e.Text)
		genericLen += utf8.RuneCountInString(g.Text)
	}

	if ruleLen >= genericLen {
		return 0, fmt.Errorf("trimmer: removes nothing generic extraction keeps")
	}

	return 1 - float64(ruleLen)/float64(genericLen), nil
}
