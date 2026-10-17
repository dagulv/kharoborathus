package tagging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	promoteInterval = time.Hour
	supportWindow   = 30
	supportPosts    = 3
	supportDomains  = 2
	maxCandidates   = 5
	sampleDays      = 7
	sampleLimit     = 200
	minSample       = 20
	gateMaxRate     = 0.2
)

func RunPromotion(ctx context.Context, s *Store, m *robot.TagMerger, t *robot.Tagger, sched schedule.Schedule, l *slog.Logger) error {
	for range sched.Tick(ctx, promoteInterval) {
		if err := promoteTags(ctx, s, m, t, l); err != nil {
			l.Error("promote tags", "err", err)
		}
	}

	return ctx.Err()
}

func promoteTags(ctx context.Context, s *Store, m *robot.TagMerger, t *robot.Tagger, l *slog.Logger) error {
	candidates, err := s.ListCandidates(ctx, supportWindow, supportPosts, supportDomains, maxCandidates)
	if err != nil {
		return fmt.Errorf("err list candidates: %w", err)
	}

	for _, c := range candidates {
		err := promoteCandidate(ctx, s, m, t, l, c)

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("promote candidate", "key", c.Key, "err", err)
		}
	}

	return nil
}

func promoteCandidate(ctx context.Context, s *Store, m *robot.TagMerger, t *robot.Tagger, l *slog.Logger, c Candidate) error {
	terms := termsFor(c.Key, c.Terms)
	candidate := robot.Tag{Key: c.Key, Terms: words(terms)}

	similar, err := s.Similar(ctx, terms)
	if err != nil {
		return fmt.Errorf("err similar tags: %w", err)
	}

	for _, existing := range similar {
		same, err := m.Same(ctx, candidate, existing)
		if err != nil {
			return fmt.Errorf("err merge check: %w", err)
		}

		if same {
			l.Info("tag aliased", "alias", c.Key, "key", existing.Key)
			return s.AddAlias(ctx, c.Key, existing.Key, terms)
		}
	}

	supporting, err := s.GetPosts(ctx, c.PostIDs)
	if err != nil {
		return fmt.Errorf("err supporting posts: %w", err)
	}

	matched, err := matching(ctx, t, candidate, supporting)
	if err != nil {
		return fmt.Errorf("err consistency check: %w", err)
	}

	if !consistent(len(matched), len(supporting)) {
		l.Info("tag dropped", "key", c.Key, "matched", len(matched), "supporting", len(supporting))
		return s.DropProposals(ctx, c.Key)
	}

	sample, err := s.ListRecentPosts(ctx, sampleDays, sampleLimit)
	if err != nil {
		return fmt.Errorf("err sample posts: %w", err)
	}

	hits, err := matching(ctx, t, candidate, sample)
	if err != nil {
		return fmt.Errorf("err broadness check: %w", err)
	}

	p := Promotion{
		Key:       c.Key,
		Terms:     terms,
		Gate:      gateFor(len(hits), len(sample)),
		MatchRate: matchRate(len(hits), len(sample)),
		Matched:   append(matched, hits...),
	}

	l.Info("tag promoted", "key", p.Key, "gate", p.Gate, "matchRate", p.MatchRate, "backfilled", len(p.Matched))

	return s.PromoteTag(ctx, p)
}

func matching(ctx context.Context, t *robot.Tagger, tag robot.Tag, posts []Post) ([]string, error) {
	var ids []string

	for _, p := range posts {
		keys, err := t.Tags(ctx, p.Title, p.Content, []robot.Tag{tag})
		if err != nil {
			return nil, fmt.Errorf("err classify post %s: %w", p.ID, err)
		}

		if len(keys) > 0 {
			ids = append(ids, p.ID)
		}
	}

	return ids, nil
}

func consistent(matched, total int) bool {
	return total > 0 && matched*3 >= total*2
}

func gateFor(hits, total int) bool {
	return total >= minSample && matchRate(hits, total) < gateMaxRate
}

func matchRate(hits, total int) float64 {
	if total == 0 {
		return 0
	}

	return float64(hits) / float64(total)
}

func words(terms []Term) []string {
	out := make([]string, len(terms))

	for i, t := range terms {
		out[i] = t.Term
	}

	return out
}
