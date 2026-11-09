package enrich

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	discovery "github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

const (
	maxRetries   = 10
	maxProposals = 5
)

type Service struct {
	DB       *database.Database
	RawPosts *postgres.RawPostStore
	Posts    *postgres.PostStore
	Tags     *postgres.TagStore
	LLM      port.LLM
	// Nil when Jev is not configured. Posts then get proposed keys only.
	Classifier port.Classifier
	SearchIDs  chan<- string
	// New discovery_posts ids, for discovered ingest.
	DiscoveredIDs chan<- string

	tags tagCache
}

type answer struct {
	Summary string              `json:"summary"`
	Tags    []postgres.Proposal `json:"tags"`
	Events  []discovery.Keyword `json:"events"`
}

// A closed schedule or shutdown is not an attempt. Any other error fails the raw post and counts a retry.
func (s *Service) Enrich(ctx context.Context, l *slog.Logger, rawPostID string) error {
	found, err := s.enrich(ctx, l, rawPostID)
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// The window can close while Jev runs. That is a closed schedule, not a failure.
	if errors.Is(err, port.ErrPeakHours) {
		return nil
	}

	if err != nil {
		if ferr := s.RawPosts.FailEnrich(context.WithoutCancel(ctx), rawPostID); ferr != nil {
			return fmt.Errorf("%w, and %w", err, ferr)
		}

		return err
	}

	for _, id := range found.Queries {
		discovery.Enqueue(s.SearchIDs, l, id)
	}

	for _, id := range found.Links {
		discovery.EnqueueLink(s.DiscoveredIDs, l, id)
	}

	return nil
}

func (s *Service) enrich(ctx context.Context, l *slog.Logger, rawPostID string) (discovery.Found, error) {
	p, err := s.RawPosts.GetRawPost(ctx, rawPostID)
	if err != nil {
		return discovery.Found{}, err
	}

	if p.Status != domain.StatusAccepted && (p.Status != domain.StatusFailed || p.RetryCount >= maxRetries) {
		return discovery.Found{}, nil
	}

	exists, err := s.Posts.PostExists(ctx, p.URL)
	if err != nil || exists {
		return discovery.Found{}, err
	}

	if !s.LLM.Schedule().Active(time.Now()) {
		return discovery.Found{}, nil
	}

	l = l.With("raw_post_id", p.ID, "url", p.URL)

	set, err := s.tags.get(ctx, p.Lang, s.loadTags)
	if err != nil {
		return discovery.Found{}, err
	}

	active := set.active

	var tags []string

	// Before DeepSeek, so a Jev outage never wastes a paid call.
	if s.Classifier != nil {
		if tags, err = jevTags(ctx, s.Classifier, l, p.Title, p.Content, active); err != nil {
			return discovery.Found{}, err
		}
	}

	var quality []string

	for _, t := range active {
		if t.Type == domain.TagTypeQuality {
			quality = append(quality, t.Key)
		}
	}

	prompt, err := userPrompt(p.Lang, set.keys, quality, p.Content)
	if err != nil {
		return discovery.Found{}, err
	}

	temp := 0.2

	var a answer

	if err := s.LLM.Unmarshal(ctx, l, port.Request{System: systemPrompt, Prompt: prompt, Temperature: &temp}, &a); err != nil {
		return discovery.Found{}, fmt.Errorf("err enrich answer: %w", err)
	}

	trusted, err := s.Posts.Trusted(ctx, p.SourceID)
	if err != nil {
		return discovery.Found{}, err
	}

	proposals := make([]postgres.Proposal, 0, maxProposals)

	for _, t := range a.Tags {
		t.Key = discovery.TagKey(t.Key)
		if strings.Trim(t.Key, "_") == "" || len(proposals) == maxProposals {
			continue
		}

		proposals = append(proposals, t)
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

	var found discovery.Found

	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if trusted {
			proposed, err := s.Tags.SaveProposals(ctx, tx, p.Lang, proposals)
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

		found, err = discovery.Discover(ctx, tx, post, a.Events)
		return err
	})
	if err != nil {
		return discovery.Found{}, fmt.Errorf("err save post: %w", err)
	}

	return found, nil
}

func (s *Service) loadTags(ctx context.Context, lang string) (tagSet, error) {
	active, err := s.Tags.ActiveTags(ctx, lang)
	if err != nil {
		return tagSet{}, err
	}

	keys, err := s.Tags.TagKeys(ctx)
	if err != nil {
		return tagSet{}, err
	}

	return tagSet{keys: keys, active: active}, nil
}
