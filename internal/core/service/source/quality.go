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

func isArticle(ctx context.Context, c port.Classifier, calls *slog.Logger, title, markdown string) (float64, error) {
	if r := []rune(markdown); len(r) > maxArticleState {
		markdown = string(r[:maxArticleState])
	}

	scores, err := c.Noul(ctx, calls, map[string]string{"title": title, "article": markdown}, articleQuestion)
	if err != nil {
		return 0, fmt.Errorf("err classify article: %w", err)
	}

	return scores["article"], nil
}

// A failed check leaves the post pending for retryPending. An error never rejects a post.
func (s *Service) checkQuality(ctx context.Context, l *slog.Logger, p domain.RawPost) (domain.RawPost, error) {
	duplicate, err := s.RawPostStore.HasDuplicate(ctx, p)
	if err != nil {
		return p, err
	}

	dupIn, dupOut := map[string]any{"simhash": p.Simhash}, map[string]any{"duplicate": duplicate}

	if duplicate {
		s.step(ctx, p.ID, domain.StepDuplicateCheck, domain.OutcomeStopped, "near duplicate of an earlier post", dupIn, dupOut)
		p.Status = domain.StatusDuplicate
		return p, nil
	}

	s.step(ctx, p.ID, domain.StepDuplicateCheck, domain.OutcomePassed, "", dupIn, dupOut)

	if s.Classifier == nil {
		s.step(ctx, p.ID, domain.StepQualityCheck, domain.OutcomePassed, "quality check off", nil, nil)
		p.Status = domain.StatusAccepted
		return p, nil
	}

	input := map[string]any{"title": p.Title, "contentChars": len([]rune(p.Content))}

	score, err := isArticle(ctx, s.Classifier, s.callsFor(p.ID, domain.StepQualityCheck), p.Title, p.Content)
	if err != nil {
		l.Warn("check quality", "source", p.SourceID, "url", p.URL, "err", err)
		s.step(ctx, p.ID, domain.StepQualityCheck, domain.OutcomeError, err.Error(), input, nil)
		return p, fmt.Errorf("%w: %w", pipeline.ErrSkip, err)
	}

	output := map[string]any{"score": score, "threshold": articleThreshold}

	if score < articleThreshold {
		s.step(ctx, p.ID, domain.StepQualityCheck, domain.OutcomeStopped, fmt.Sprintf("score %.2f, needs %.2f", score, articleThreshold), input, output)
		p.Status = domain.StatusRejected
		return p, nil
	}

	s.step(ctx, p.ID, domain.StepQualityCheck, domain.OutcomePassed, "", input, output)
	p.Status = domain.StatusAccepted

	return p, nil
}
