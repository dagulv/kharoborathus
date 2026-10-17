package source

import "testing"

func TestFindListing(t *testing.T) {
	cases := []struct {
		name, page, adapter, url string
	}{
		{"rss", `<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`, "rss", "https://www.a.example/feed.xml"},
		{"atom", `<html><head><link rel="alternate" type="application/atom+xml" href="https://feeds.example/a"></head></html>`, "rss", "https://feeds.example/a"},
		{"first wins", `<html><head><link rel="alternate" type="application/rss+xml" href="/one"><link rel="alternate" type="application/rss+xml" href="/two"></head></html>`, "rss", "https://www.a.example/one"},
		{"rel list", `<html><head><link rel="alternate nofollow" type="application/rss+xml" href="/f"></head></html>`, "rss", "https://www.a.example/f"},
		{"other alternate", `<html><head><link rel="alternate" hreflang="en" href="/en"></head></html>`, "page", "https://a.example"},
		{"empty href", `<html><head><link rel="alternate" type="application/rss+xml" href=""></head></html>`, "page", "https://a.example"},
		{"none", `<html><body>Hej</body></html>`, "page", "https://a.example"},
	}

	for _, c := range cases {
		adapter, url := findListing([]byte(c.page), "https://www.a.example/", "https://a.example")
		if adapter != c.adapter || url != c.url {
			t.Errorf("%s: got %s %s", c.name, adapter, url)
		}
	}
}
