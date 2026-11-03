package html

import (
	"slices"
	"strings"
	"testing"
)

func TestContentRule(t *testing.T) {
	s := site{body: "article-body", updated: "Uppdaterad 08:12"}

	e, err := content(goodRule(), FieldRule{}, []byte(s.article(4)))
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

	if strings.Contains(e.Content, "class=") || strings.Contains(e.Content, "data-testid") {
		t.Errorf("attributes should be stripped: %s", e.Content)
	}
}

func TestContentRuleFailures(t *testing.T) {
	s := site{body: "article-body", updated: "Uppdaterad 08:12"}
	page := []byte(s.article(1))

	cases := map[string]struct {
		rule func(ContentRule) ContentRule
		fail string
	}{
		"no match": {func(r ContentRule) ContentRule { r.Include = str("div.story"); return r }, FailIncludeNone},
		"many":     {func(r ContentRule) ContentRule { r.Include = str("p"); return r }, FailIncludeMany},
		"title": {func(r ContentRule) ContentRule {
			r.Include = str("div.article-wrap")
			r.Exclude = []string{"aside", ".comments", ".promo", ".newsletter-cta", ".updated", ".byline", "time"}
			return r
		}, FailTitleRepeated},
		"strip": {func(r ContentRule) ContentRule { r.StripAfter = str("p"); return r }, FailTooShort},
	}

	for name, c := range cases {
		e := contentRule(mustCompile(t, c.rule(goodRule())), FieldRule{}, page)

		if !slices.Contains(e.Failures, c.fail) {
			t.Errorf("%s: expected %s, got %v", name, c.fail, e.Failures)
		}
	}
}

func TestImagePolicy(t *testing.T) {
	s := site{body: "article-body", updated: "x"}
	r := goodRule()
	r.Exclude = []string{".newsletter-cta"}

	if e := contentRule(mustCompile(t, r), FieldRule{}, []byte(s.article(1))); !strings.Contains(e.Content, "<img") {
		t.Error("keep dropped the image")
	}

	r.ImagePolicy = ImageDrop

	if e := contentRule(mustCompile(t, r), FieldRule{}, []byte(s.article(1))); strings.Contains(e.Content, "<img") || strings.Contains(e.Content, "<figure") {
		t.Error("drop kept the image")
	}
}

func TestSanity(t *testing.T) {
	long := strings.Repeat("Ett stycke text om något viktigt. ", 20)

	cases := map[string]struct {
		e    extraction
		fail string
	}{
		"paywall":      {extraction{Text: long + "Bli prenumerant för att läsa vidare."}, FailPaywall},
		"unterminated": {extraction{Text: long + "och sedan"}, FailUnterminated},
		"links":        {extraction{Text: long, linkDensity: 0.3}, FailLinkDensity},
		"short":        {extraction{Text: "Kort."}, FailTooShort},
		"title":        {extraction{Text: "Rubriken här\n\n" + long, Title: "Rubriken här!"}, FailTitleRepeated},
	}

	for name, c := range cases {
		sanity(&c.e)

		if !slices.Contains(c.e.Failures, c.fail) {
			t.Errorf("%s: expected %s, got %v", name, c.fail, c.e.Failures)
		}
	}

	ok := extraction{Text: long + `"Citat."`}
	if sanity(&ok); !ok.OK() {
		t.Errorf("expected ok, got %v", ok.Failures)
	}
}

func mustCompile(t *testing.T, r ContentRule) *compiledRule {
	t.Helper()

	c, err := compile(r)
	if err != nil {
		t.Fatal(err)
	}

	return c
}
