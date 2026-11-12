package html

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

func TestExtractorFields(t *testing.T) {
	ctx := context.Background()
	s := site{body: "article-body", updated: "x"}
	page := []byte(s.article(4))

	rules := map[field]any{
		fieldContent:  goodRule(),
		fieldTitle:    FieldRule{Selector: str("h1.headline")},
		fieldAuthors:  FieldRule{Selector: str(".byline a")},
		fieldPostedAt: FieldRule{Selector: str("time[datetime]")},
	}

	d := domain.Domain{ID: "ruled", Metadata: domain.Metadata{Extract: make(map[string]json.RawMessage)}}
	for f, r := range rules {
		d.Metadata.Extract[string(f)], _ = json.Marshal(r)
	}

	tr := newExtractor(&idleLLM{}, nil, nil, nil, discard)

	content, err := tr.Content(ctx, discard, d, s.url(4), page)
	if err != nil || strings.Contains(content, "Läs också") || !strings.Contains(content, "Artikel 4") {
		t.Errorf("content %v: %s", err, content)
	}

	title, _ := tr.Title(ctx, discard, d, s.url(4), page)
	authors, _ := tr.Authors(ctx, discard, d, s.url(4), page)
	postedAt, _ := tr.PostedAt(ctx, discard, d, s.url(4), page)

	if title != "Rubrik nummer 4 om inflationen" || !slices.Equal(authors, []string{"Anna Svensson"}) || postedAt.Day() != 5 {
		t.Errorf("fields %q %v %v", title, authors, postedAt)
	}

	if len(tr.samples) != 0 {
		t.Errorf("collected %d domains with every rule stored", len(tr.samples))
	}

	bare := domain.Domain{ID: "bare"}

	if content, err := tr.Content(ctx, discard, bare, s.url(4), page); err != nil || !strings.Contains(content, "Artikel 4") {
		t.Errorf("generic content %v: %s", err, content)
	}

	if _, err := tr.Authors(ctx, discard, bare, s.url(4), page); err != nil {
		t.Fatal(err)
	}

	if len(tr.samples[bare.ID]) != 1 || len(tr.requested[bare.ID]) != 2 {
		t.Errorf("%d samples, requested %v", len(tr.samples[bare.ID]), tr.requested[bare.ID])
	}
}
