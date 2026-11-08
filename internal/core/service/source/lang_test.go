package source

import "testing"

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{"sv-SE": "sv", "en_us": "en", "EN": "en", " sv ": "sv", "": ""}

	for in, want := range cases {
		if got := normalizeLang(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}
