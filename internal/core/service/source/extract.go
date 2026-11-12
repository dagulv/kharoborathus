package source

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

// Each field keeps what the fetcher found. The fetcher's content stays when extraction fails or finds less text.
func (s *Service) Extract(ctx context.Context, l *slog.Logger, p domain.RawPost) (domain.RawPost, error) {
	d, err := s.DomainStore.GetOrCreateDomain(ctx, p.URL)
	if err != nil {
		return p, fmt.Errorf("err get domain %s: %w", p.URL, err)
	}

	p.DomainID = d.ID

	if p.RawContent != "" {
		p = s.extract(ctx, l, d, p)
	}

	if p.Content == "" {
		l.Debug("no content", "source", p.SourceID, "url", p.URL)
		return p, pipeline.ErrSkip
	}

	return p, nil
}

func (s *Service) extract(ctx context.Context, l *slog.Logger, d domain.Domain, p domain.RawPost) domain.RawPost {
	page := []byte(p.RawContent)
	l = l.With("domain", d.BaseURL, "url", p.URL)

	if content, err := s.Extractor.Content(ctx, l, d, p.URL, page); err != nil {
		l.Debug("extract content", "err", err)
	} else if plainLen(content) > plainLen(p.Content) {
		p.Content = content
	}

	if p.Title == "" {
		title, err := s.Extractor.Title(ctx, l, d, p.URL, page)
		if err != nil {
			l.Error("extract title", "err", err)
		}

		p.Title = title
	}

	if len(p.Authors) == 0 {
		authors, err := s.Extractor.Authors(ctx, l, d, p.URL, page)
		if err != nil {
			l.Error("extract authors", "err", err)
		}

		p.Authors = authors
	}

	if p.PostedAt == nil {
		postedAt, err := s.Extractor.PostedAt(ctx, l, d, p.URL, page)
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
