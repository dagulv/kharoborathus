package tagging

import (
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot"
)

func testVocabulary() Vocabulary {
	return Vocabulary{
		Tags: map[string]TagState{
			"investment": {Enabled: true, Status: statusActive},
			"layoffs":    {Enabled: true, Status: statusDormant},
			"bullish":    {Enabled: false, Status: statusActive},
			"dividend":   {Enabled: true, Status: statusActive},
		},
		Aliases: map[string]string{"investments": "investment"},
	}
}

func TestRoute(t *testing.T) {
	r := route(testVocabulary(), "sv", []robot.GeneratedTag{
		{Key: "investments", Term: "investeringar"},
		{Key: "investment", Term: "investering"},
		{Key: "layoffs", Term: "varsel"},
		{Key: "bullish", Term: "uppåt"},
		{Key: "share_buyback", Term: "återköp"},
	})

	wantTerms := []Term{
		{Key: "investment", Lang: "en", Term: "investment"},
		{Key: "investment", Lang: "sv", Term: "investeringar"},
		{Key: "layoffs", Lang: "en", Term: "layoffs"},
		{Key: "layoffs", Lang: "sv", Term: "varsel"},
	}

	if !slices.Equal(r.terms, wantTerms) {
		t.Errorf("terms %+v", r.terms)
	}

	if !slices.Equal(r.reactivate, []string{"layoffs"}) {
		t.Errorf("reactivate %v", r.reactivate)
	}

	if !slices.Equal(r.proposals, []Term{{Key: "share_buyback", Lang: "sv", Term: "återköp"}}) {
		t.Errorf("proposals %+v", r.proposals)
	}
}

func TestRouteEnglish(t *testing.T) {
	r := route(testVocabulary(), "en", []robot.GeneratedTag{
		{Key: "dividend", Term: "dividend"},
		{Key: "share_buyback", Term: "share buyback"},
	})

	if !slices.Equal(r.terms, []Term{{Key: "dividend", Lang: "en", Term: "dividend"}}) {
		t.Errorf("terms %+v", r.terms)
	}

	if !slices.Equal(r.proposals, []Term{{Key: "share_buyback", Lang: "en", Term: "share buyback"}}) {
		t.Errorf("proposals %+v", r.proposals)
	}
}

func TestActiveKeys(t *testing.T) {
	if keys := testVocabulary().ActiveKeys(); !slices.Equal(keys, []string{"dividend", "investment"}) {
		t.Errorf("keys %v", keys)
	}
}

func TestTermsFor(t *testing.T) {
	got := termsFor("share_buyback", []Term{
		{Key: "share_buyback", Lang: "sv", Term: "återköp"},
		{Key: "share_buyback", Lang: "en", Term: "share buyback"},
		{Key: "share_buyback", Lang: "sv", Term: "återköp"},
		{Key: "share_buyback", Lang: "", Term: "buyback"},
		{Key: "share_buyback", Lang: "fi", Term: "takaisinosto"},
	})

	want := []Term{
		{Key: "share_buyback", Lang: "en", Term: "share buyback"},
		{Key: "share_buyback", Lang: "sv", Term: "återköp"},
		{Key: "share_buyback", Lang: "fi", Term: "takaisinosto"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("terms %+v", got)
	}
}
