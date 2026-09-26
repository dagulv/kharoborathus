package domain

import "testing"

func TestBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://www.dn.se/ekonomi/artikel-1?utm=x": "https://dn.se",
		"https://DN.se":                   "https://dn.se",
		"http://blogg.example.com:8080/a": "http://blogg.example.com:8080",
		"//www.svd.se/a":                  "https://svd.se",
	}

	for in, want := range cases {
		if got, err := BaseURL(in); err != nil || got != want {
			t.Errorf("%q: got %q, %v, want %q", in, got, err, want)
		}
	}

	for _, in := range []string{"/relative/path", "", "::"} {
		if _, err := BaseURL(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}
