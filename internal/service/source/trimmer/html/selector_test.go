package html

import (
	"errors"
	"testing"
)

func TestParseSelector(t *testing.T) {
	valid := []string{
		"div.article-body",
		"article",
		"#main",
		"time[datetime]",
		`div[data-testid="article-body"]`,
		"[itemprop=articleBody]",
		".byline a",
		"div.wrap > div.body",
		"h1 + p",
		"h2 ~ ul",
		"div.a.b#c",
	}

	for _, s := range valid {
		if _, err := parseSelector(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}

	invalid := []string{
		"",
		"p:nth-child(2)",
		"p:contains(foo)",
		"div, p",
		"//div[@class='x']",
		"*",
		"div >",
		"div > > p",
		`[class*="body"]`,
		`[href^="http"]`,
		".a\\:b",
		"div.",
		"p::before",
		"a b c d e f g",
	}

	for _, s := range invalid {
		if _, err := parseSelector(s); !errors.Is(err, ErrSelectorSyntax) {
			t.Errorf("%q: expected syntax error, got %v", s, err)
		}
	}
}

func TestGround(t *testing.T) {
	v := newVocabulary()
	v.tags["div"] = true
	v.tags["time"] = true
	v.tags["a"] = true
	v.classes["article-body"] = true
	v.classes["byline"] = true
	v.classes["css-1x7ftz9"] = true
	v.addAttr("datetime", "")
	v.addAttr("data-testid", "article")

	grounded := []string{"div.article-body", "time[datetime]", ".byline a", `[data-testid="article"]`}

	for _, s := range grounded {
		if err := v.ground(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}

	ungrounded := []string{
		"div.story-body",
		"section.article-body",
		"#content",
		"div.css-1x7ftz9",
		`[data-testid="story"]`,
		"[itemprop]",
		`div[style="display:block"]`,
	}

	for _, s := range ungrounded {
		if err := v.ground(s); err == nil {
			t.Errorf("%q: expected grounding error", s)
		}
	}
}

func TestHashed(t *testing.T) {
	for _, s := range []string{"css-1x7ftz9", "sc-bdVaJa", "Article_body__3xYz1", "svelte-1q2w3e", "a1b2c3"} {
		if !hashed(s) {
			t.Errorf("%q should look hashed", s)
		}
	}

	for _, s := range []string{"article-body", "col-md-12", "h1", "headline", "post-2024", "byline", "mt-4", "article__body"} {
		if hashed(s) {
			t.Errorf("%q should look stable", s)
		}
	}
}

func TestRuleGround(t *testing.T) {
	s := site{body: "article-body", updated: "Uppdaterad 08:12"}

	var docs []document
	for i := range 3 {
		d, err := parse([]byte(s.article(i)))
		if err != nil {
			t.Fatal(err)
		}

		docs = append(docs, d)
	}

	skel := buildSkeleton(docs)

	if err := goodRule().ground(skel.vocab); err != nil {
		t.Fatalf("good rule: %v", err)
	}

	r := goodRule()
	r.Exclude = append(r.Exclude, ".share-bar")
	if err := r.ground(skel.vocab); !errors.Is(err, ErrSelectorUngrounded) {
		t.Fatalf("invented exclude: %v", err)
	}

	r = goodRule()
	r.Exclude = make([]string, maxExclude+1)
	for i := range r.Exclude {
		r.Exclude[i] = ".promo"
	}
	if err := r.ground(skel.vocab); err == nil {
		t.Fatal("too many excludes accepted")
	}
}
