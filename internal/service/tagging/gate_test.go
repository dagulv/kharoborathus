package tagging

import "testing"

func gateVocabulary() Vocabulary {
	return Vocabulary{
		Tags: map[string]TagState{
			"acquisition":   {Enabled: true, Status: statusActive, Gate: true},
			"investment":    {Enabled: true, Status: statusActive, Gate: true},
			"earnings":      {Enabled: true, Status: statusActive},
			"share_buyback": {Enabled: true, Status: statusActive},
			"layoffs":       {Enabled: true, Status: statusDormant, Gate: true},
			"bankruptcy":    {Enabled: false, Status: statusActive, Gate: true},
		},
		Aliases: map[string]string{"investments": "investment"},
	}
}

func TestShadowGate(t *testing.T) {
	cases := []struct {
		name          string
		targets, keys []string
		want          bool
	}{
		{"target hit", []string{"earnings"}, []string{"earnings"}, true},
		{"target miss despite gate tag", []string{"earnings"}, []string{"acquisition"}, false},
		{"alias target", []string{"investments"}, []string{"investment"}, true},
		{"no targets, gate tag", nil, []string{"earnings", "acquisition"}, true},
		{"no targets, only non-gate tag", nil, []string{"earnings"}, false},
		{"no targets, disabled gate tag", nil, []string{"bankruptcy"}, false},
		{"no targets, dormant gate tag", nil, []string{"layoffs"}, false},
		{"no targets, no tags", nil, nil, false},
		{"typed target", []string{" Share-Buyback "}, []string{"share_buyback"}, true},
		{"spaced target", []string{"share  buyback"}, []string{"share_buyback"}, true},
		{"unknown target falls back to gate tags", []string{"finance"}, []string{"acquisition"}, true},
		{"unknown target, no gate tag", []string{"finance"}, []string{"earnings"}, false},
		{"disabled target falls back to gate tags", []string{"bankruptcy"}, []string{"investment"}, true},
	}

	for _, c := range cases {
		got := shadowGate(gateVocabulary(), c.targets, c.keys)
		if got == nil || *got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestShadowGateWithoutGateTags(t *testing.T) {
	v := Vocabulary{Tags: map[string]TagState{
		"earnings":   {Enabled: true, Status: statusActive},
		"layoffs":    {Enabled: true, Status: statusDormant, Gate: true},
		"bankruptcy": {Enabled: false, Status: statusActive, Gate: true},
	}}

	for _, targets := range [][]string{nil, {"finance"}} {
		if got := shadowGate(v, targets, []string{"earnings"}); got != nil {
			t.Errorf("targets %v: got %v, want nil", targets, *got)
		}
	}
}
