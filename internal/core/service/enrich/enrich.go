package enrich

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	discovery "github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const maxRetries = 10

type Service struct {
	DB       *database.Database
	RawPosts *postgres.RawPostStore
	Posts    *postgres.PostStore
	Tags     *postgres.TagStore
	LLM      port.LLM
	// Nil when Jev is not configured. Posts then get proposed keys only.
	Classifier port.Classifier
	SearchIDs  chan<- string
}

type answer struct {
	Summary string              `json:"summary"`
	Tags    []postgres.Proposal `json:"tags"`
	Events  []discovery.Keyword `json:"events"`
}

// A closed schedule or shutdown is not an attempt. Any other error fails the raw post and counts a retry.
func (s *Service) Enrich(ctx context.Context, l *slog.Logger, rawPostID string) error {
	queryIDs, err := s.enrich(ctx, l, rawPostID)
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if err != nil {
		if ferr := s.RawPosts.FailEnrich(context.WithoutCancel(ctx), rawPostID); ferr != nil {
			return fmt.Errorf("%w, and %w", err, ferr)
		}

		return err
	}

	for _, id := range queryIDs {
		discovery.Enqueue(s.SearchIDs, l, id)
	}

	return nil
}

func (s *Service) enrich(ctx context.Context, l *slog.Logger, rawPostID string) ([]string, error) {
	p, err := s.RawPosts.GetRawPost(ctx, rawPostID)
	if err != nil {
		return nil, err
	}

	if p.Status != domain.StatusAccepted && (p.Status != domain.StatusFailed || p.RetryCount >= maxRetries) {
		return nil, nil
	}

	exists, err := s.Posts.PostExists(ctx, p.URL)
	if err != nil || exists {
		return nil, err
	}

	if !s.LLM.Schedule().Active(time.Now()) {
		return nil, nil
	}

	l = l.With("raw_post_id", p.ID, "url", p.URL)

	active, err := s.Tags.ActiveTags(ctx, p.Lang)
	if err != nil {
		return nil, err
	}

	var tags []string

	// Before DeepSeek, so a Jev outage never wastes a paid call.
	if s.Classifier != nil {
		if tags, err = jevTags(ctx, s.Classifier, l, p.Title, p.Content, active); err != nil {
			return nil, err
		}
	}

	keys, err := s.Tags.TagKeys(ctx)
	if err != nil {
		return nil, err
	}

	var quality []string

	for _, t := range active {
		if t.Type == domain.TagTypeQuality {
			quality = append(quality, t.Key)
		}
	}

	prompt, err := userPrompt(p.Lang, keys, quality, p.Content)
	if err != nil {
		return nil, err
	}

	temp := 0.2

	var a answer

	if err := s.LLM.Unmarshal(ctx, l, port.Request{System: systemPrompt, Prompt: prompt, Temperature: &temp}, &a); err != nil {
		return nil, fmt.Errorf("err enrich answer: %w", err)
	}

	trusted, err := s.Posts.Trusted(ctx, p.SourceID)
	if err != nil {
		return nil, err
	}

	for i := range a.Tags {
		a.Tags[i].Key = discovery.TagKey(a.Tags[i].Key)
	}

	summary := a.Summary
	if summary == "" {
		summary = excerpt(p.Content)
	}

	post := domain.Post{
		SourceID:     p.SourceID,
		URL:          p.URL,
		Lang:         p.Lang,
		DomainID:     p.DomainID,
		Title:        p.Title,
		ShortContent: summary,
		Content:      p.Content,
		PostedAt:     p.PostedAt,
		Authors:      p.Authors,
		Simhash:      p.Simhash,
	}

	var queryIDs []string

	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if trusted {
			proposed, err := s.Tags.SaveProposals(ctx, tx, p.Lang, a.Tags)
			if err != nil {
				return err
			}

			for _, k := range proposed {
				if !slices.Contains(tags, k) {
					tags = append(tags, k)
				}
			}
		}

		post.Tags = tags

		post, inserted, err := s.Posts.InsertPost(ctx, tx, post)
		if err != nil || !inserted {
			return err
		}

		queryIDs, err = discovery.Discover(ctx, tx, post, a.Events)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("err save post: %w", err)
	}

	return queryIDs, nil
}
