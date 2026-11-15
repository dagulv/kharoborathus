package source

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

const (
	articleThreshold = 0.8
	maxArticleState  = 60_000
)

var articleQuestion = map[string]port.NoulQuestion{
	"article": {
		Instructions: "Does this page have substantive content of its own, like a news article, blog post, report, analysis or essay?",
		Criteria: &port.NoulCriteria{
			True:  "It makes specific points about a named subject, with concrete details, examples, findings or a conclusion.",
			False: "It is a listing, index, category, product, login or error page, or its text is too thin or generic to say anything specific.",
		},
	},
}

func isArticle(ctx context.Context, c port.Classifier, l *slog.Logger, title, markdown string) (bool, error) {
	if r := []rune(markdown); len(r) > maxArticleState {
		markdown = string(r[:maxArticleState])
	}

	scores, err := c.Noul(ctx, l, map[string]string{"title": title, "article": markdown}, articleQuestion)
	if err != nil {
		return false, fmt.Errorf("err classify article: %w", err)
	}

	return scores["article"] >= articleThreshold, nil
}

// A failed check leaves the post pending for retryPending. An error never rejects a post.
func (s *Service) checkQuality(ctx context.Context, l *slog.Logger, p domain.RawPost) (domain.RawPost, error) {
	duplicate, err := s.RawPostStore.HasDuplicate(ctx, p)
	if err != nil {
		return p, err
	}

	if duplicate {
		p.Status = domain.StatusDuplicate
		return p, nil
	}

	if s.Classifier == nil {
		p.Status = domain.StatusAccepted
		return p, nil
	}

	article, err := isArticle(ctx, s.Classifier, l.With("source_id", p.SourceID, "url", p.URL), p.Title, p.Content)
	if err != nil {
		l.Warn("check quality", "source", p.SourceID, "url", p.URL, "err", err)
		return p, fmt.Errorf("%w: %w", pipeline.ErrSkip, err)
	}

	p.Status = domain.StatusAccepted
	if !article {
		p.Status = domain.StatusRejected
	}

	return p, nil
}
