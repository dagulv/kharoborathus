package ingest

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestOutboundLinks(t *testing.T) {
	md := `Volvo [köper](https://news.example/a?utm_source=x&id=7#top) Northvolt.

![bild](https://img.example/b.jpg)

[Egen sida](https://own.example/om) och [www](http://www.own.example/x).

[Relativ](/c) [mejl](mailto:a@b.example) [ftp](ftp://files.example/f)

[Facebook](https://m.facebook.com/volvo) [Tube](https://youtu.be/x) [Drive](https://drive.google.com/file/d/1)

[Igen](https://news.example/a?id=7) [Titel](https://other.example/d "Rubrik")

[Spår](https://other.example/e?utm_medium=m)`

	got := outboundLinks(md, "https://www.own.example")
	want := []string{"https://news.example/a?id=7", "https://other.example/d", "https://other.example/e"}

	if !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestOutboundLinksCap(t *testing.T) {
	var b strings.Builder

	for i := range 15 {
		fmt.Fprintf(&b, "[l%d](https://news.example/%d) ", i, i)
	}

	got := outboundLinks(b.String(), "https://own.example")

	if len(got) != maxLinksPerPost || got[0] != "https://news.example/0" || got[9] != "https://news.example/9" {
		t.Errorf("got %v", got)
	}
}

func TestOutboundLinksNone(t *testing.T) {
	if got := outboundLinks("Inga länkar här.", "https://own.example"); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

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
	got := FilterLinks([]string{
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

	if got := FilterLinks([]string{long, "https://news.example/b"}, "https://own.example"); !slices.Equal(got, []string{"https://news.example/b"}) {
		t.Errorf("got %v", got)
	}
}
