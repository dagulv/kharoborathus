package discovery

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://WWW.A.example/x": "a.example",
		"http://a.example:8080/x": "a.example",
		"https://news.a.example":  "news.a.example",
		"not a url %zz":           "",
	}

	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}

func TestFilterLinks(t *testing.T) {
	got := filterLinks([]string{
		"https://news.example/a?utm_source=x#top",
		"https://www.own.example/x",
		"https://www.youtube.com/watch?v=1",
		"ftp://files.example/f",
		"https://news.example/a",
		"https://other.example/b",
	}, "https://own.example")

	if want := []string{"https://news.example/a", "https://other.example/b"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestFilterLinksTooLong(t *testing.T) {
	long := "https://news.example/a?" + strings.Repeat("x", 3000)

	if got := filterLinks([]string{long, "https://news.example/b"}, "https://own.example"); !slices.Equal(got, []string{"https://news.example/b"}) {
		t.Errorf("got %v", got)
	}
}

func TestOutboundLinks(t *testing.T) {
	md := `[a](https://news.example/a?utm_source=x) ![img](https://img.example/i.png)
[own](https://own.example/x) [t](<https://other.example/b> "title")`

	if got, want := outboundLinks(md, "https://own.example"), []string{"https://news.example/a", "https://other.example/b"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestOutboundLinksCap(t *testing.T) {
	var md strings.Builder

	for i := range 15 {
		fmt.Fprintf(&md, "[l](https://n%d.example/a) ", i)
	}

	got := outboundLinks(md.String(), "https://own.example")
	if len(got) != 10 || got[0] != "https://n0.example/a" {
		t.Errorf("got %v", got)
	}
}
