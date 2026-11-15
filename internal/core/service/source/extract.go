package source

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/core/domain"
)

// Each field keeps what the fetcher found. The fetcher's content stays when extraction fails or finds less text.
// A redirect can leave the host, so the domain comes from the final url.
func (s *Service) Extract(ctx context.Context, l *slog.Logger, p domain.RawPost) (domain.RawPost, error) {
	pageURL := cmp.Or(p.FinalURL, p.URL)

	d, err := s.DomainStore.GetOrCreateDomain(ctx, pageURL)
	if err != nil {
		return p, fmt.Errorf("err get domain %s: %w", pageURL, err)
	}

	p.DomainID = d.ID

	if p.RawContent != "" {
		// Rule generation is a domain-level call, not one of this post's.
		p = s.extract(ctx, l, s.Calls.With("domain_id", d.ID), d, p)
	}

	rules := slices.Sorted(maps.Keys(d.Metadata.Extract))
	input := map[string]any{"bytes": len(p.RawContent), "rules": rules}

	if p.Content == "" {
		l.Debug("no content", "source", p.SourceID, "url", p.URL)
		return p, s.stop(ctx, p, domain.StatusMissing, domain.StepExtract, "no content found", input, nil)
	}

	s.step(ctx, p.ID, domain.StepExtract, domain.OutcomePassed, "", input, map[string]any{
		"title":        p.Title,
		"authors":      p.Authors,
		"postedAt":     p.PostedAt,
		"contentChars": utf8.RuneCountInString(p.Content),
	})

	return p, nil
}

func (s *Service) extract(ctx context.Context, l, calls *slog.Logger, d domain.Domain, p domain.RawPost) domain.RawPost {
	page := []byte(p.RawContent)
	pageURL := cmp.Or(p.FinalURL, p.URL)
	l = l.With("domain", d.BaseURL, "url", pageURL)

	if content, err := s.Extractor.Content(ctx, calls, d, pageURL, page); err != nil {
		l.Debug("extract content", "err", err)
	} else if plainLen(content) > plainLen(p.Content) {
		p.Content = content
	}

	if p.Title == "" {
		title, err := s.Extractor.Title(ctx, calls, d, pageURL, page)
		if err != nil {
			l.Error("extract title", "err", err)
		}

		p.Title = title
	}

	if len(p.Authors) == 0 {
		authors, err := s.Extractor.Authors(ctx, calls, d, pageURL, page)
		if err != nil {
			l.Error("extract authors", "err", err)
		}

		p.Authors = authors
	}

	if p.PostedAt == nil {
		postedAt, err := s.Extractor.PostedAt(ctx, calls, d, pageURL, page)
		if err != nil {
			l.Error("extract posted at", "err", err)
		}

		if !postedAt.IsZero() {
			p.PostedAt = &postedAt
		}
	}

	return p
}

// A post that fails to convert keeps its html rather than being dropped.
func SetMarkdown(_ context.Context, l *slog.Logger, p domain.RawPost) (domain.RawPost, error) {
	md, err := htmltomarkdown.ConvertString(p.Content, converter.WithDomain(p.URL))
	if err != nil {
		l.Error("convert to markdown", "source", p.SourceID, "url", p.URL, "err", err)
		return p, nil
	}

	p.Content = md

	return p, nil
}

func plainLen(html string) int {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return 0
	}

	return utf8.RuneCountInString(doc.Text())
}
