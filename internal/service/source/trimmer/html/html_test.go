package html

import (
	"slices"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/source/trimmer"
)

func TestTrimRule(t *testing.T) {
	s := site{body: "article-body", updated: "Uppdaterad 08:12"}
	tr := New(nil, "")

	e, err := tr.Trim(goodRule(), []byte(s.article(4)))
	if err != nil {
		t.Fatal(err)
	}

	if !e.OK() {
		t.Fatalf("expected ok, got %v", e)
	}

	for _, gone := range []string{"Läs också", "inkorg", "Uppdaterad", "Relaterad", "Kommentarer"} {
		if strings.Contains(e.Text, gone) {
			t.Errorf("%q left in output", gone)
		}
	}

	if e.Title != "Rubrik nummer 4 om inflationen" || e.Author != "Anna Svensson" || e.Published.Day() != 5 {
		t.Errorf("metadata: %q %q %v", e.Title, e.Author, e.Published)
	}

	if strings.Contains(e.Content, "class=") || strings.Contains(e.Content, "data-testid") {
		t.Errorf("attributes should be stripped: %s", e.Content)
	}
}

func TestTrimRuleFailures(t *testing.T) {
	s := site{body: "article-body", updated: "Uppdaterad 08:12"}
	page := []byte(s.article(1))

	cases := map[string]struct {
		rule func(Rule) Rule
		fail string
	}{
		"no match": {func(r Rule) Rule { r.Include = str("div.story"); return r }, FailIncludeNone},
		"many":     {func(r Rule) Rule { r.Include = str("p"); return r }, FailIncludeMany},
		"title": {func(r Rule) Rule {
			r.Include = str("div.article-wrap")
			r.Exclude = []string{"aside", ".comments", ".promo", ".newsletter-cta", ".updated", ".byline", "time"}
			return r
		}, FailTitleRepeated},
		"strip": {func(r Rule) Rule { r.StripAfter = str("p"); return r }, FailTooShort},
	}

	for name, c := range cases {
		e := trimRule(mustCompile(t, c.rule(goodRule())), page)

		if !slices.Contains(e.Failures, c.fail) {
			t.Errorf("%s: expected %s, got %v", name, c.fail, e.Failures)
		}
	}
}

func TestImagePolicy(t *testing.T) {
	s := site{body: "article-body", updated: "x"}
	r := goodRule()
	r.Exclude = []string{".newsletter-cta"}

	if e := trimRule(mustCompile(t, r), []byte(s.article(1))); !strings.Contains(e.Content, "<img") {
		t.Error("keep dropped the image")
	}

	r.ImagePolicy = ImageDrop

	if e := trimRule(mustCompile(t, r), []byte(s.article(1))); strings.Contains(e.Content, "<img") || strings.Contains(e.Content, "<figure") {
		t.Error("drop kept the image")
	}
}

func TestSanity(t *testing.T) {
	long := strings.Repeat("Ett stycke text om något viktigt. ", 20)

	cases := map[string]struct {
		e    extraction
		fail string
	}{
		"paywall":      {extraction{Extraction: trimmer.Extraction{Text: long + "Bli prenumerant för att läsa vidare."}}, FailPaywall},
		"unterminated": {extraction{Extraction: trimmer.Extraction{Text: long + "och sedan"}}, FailUnterminated},
		"links":        {extraction{Extraction: trimmer.Extraction{Text: long}, linkDensity: 0.3}, FailLinkDensity},
		"short":        {extraction{Extraction: trimmer.Extraction{Text: "Kort."}}, FailTooShort},
		"title":        {extraction{Extraction: trimmer.Extraction{Text: "Rubriken här\n\n" + long, Title: "Rubriken här!"}}, FailTitleRepeated},
	}

	for name, c := range cases {
		sanity(&c.e)

		if !slices.Contains(c.e.Failures, c.fail) {
			t.Errorf("%s: expected %s, got %v", name, c.fail, c.e.Failures)
		}
	}

	ok := extraction{Extraction: trimmer.Extraction{Text: long + `"Citat."`}}
	if sanity(&ok); !ok.OK() {
		t.Errorf("expected ok, got %v", ok.Failures)
	}
}

func mustCompile(t *testing.T, r Rule) *compiledRule {
	t.Helper()

	c, err := compile(r)
	if err != nil {
		t.Fatal(err)
	}

	return c
}
