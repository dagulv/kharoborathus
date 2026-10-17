package source

import (
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/feed"
)

func TestMarkdown(t *testing.T) {
	f, err := Markdown(feed.Feed{URL: "https://a.example/news/1", Content: `<p>Volvo <a href="/x">köper</a> Northvolt.</p>`})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.Content, "[köper](https://a.example/x)") {
		t.Errorf("content %q", f.Content)
	}

	if f.ShortContent != "Volvo köper Northvolt." {
		t.Errorf("short content %q", f.ShortContent)
	}
}

func TestMarkdownKeepsShortContent(t *testing.T) {
	f, err := Markdown(feed.Feed{URL: "https://a.example/news/1", ShortContent: "Från flödet.", Content: "<p>Text.</p>"})
	if err != nil {
		t.Fatal(err)
	}

	if f.ShortContent != "Från flödet." {
		t.Errorf("short content %q", f.ShortContent)
	}
}
