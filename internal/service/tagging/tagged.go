package tagging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/iterx"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/switches"
)

const (
	tickInterval = time.Minute
	batchSize    = 20
	englishLang  = "en"
)

func Run(ctx context.Context, s *Store, sw *switches.Store, g *robot.TagGenerator, t *robot.Tagger, l, calls *slog.Logger) error {
	for range schedule.Always.Tick(ctx, tickInterval) {
		if err := tagPosts(ctx, s, sw, g, t, l, calls); err != nil {
			l.Error("tag posts", "err", err)
		}
	}

	return ctx.Err()
}

func tagPosts(ctx context.Context, s *Store, sw *switches.Store, g *robot.TagGenerator, t *robot.Tagger, l, calls *slog.Logger) error {
	shadow, err := sw.Enabled(ctx, switches.ShadowGate)
	if err != nil {
		return err
	}

	posts, err := iterx.Collect(s.ListUntaggedPosts(ctx, batchSize))
	if err != nil {
		return err
	}

	var processed, generated, failed, aiCalls int

	defer func() {
		if aiCalls > 0 {
			l.Info("tag posts", "processed", processed, "generated", generated, "failed", failed, "aiCalls", aiCalls)
		}
	}()

	for _, post := range posts {
		pl := calls.With("post_id", post.ID)
		lang := normalizeLang(post.Lang)

		v, err := s.Vocabulary(ctx)
		if err != nil {
			return fmt.Errorf("err vocabulary: %w", err)
		}

		// Only trusted posts shape the vocabulary, but every post is still classified.
		if post.Trusted {
			err = generateTags(ctx, pl, s, g, v, lang, post)
			aiCalls++
		}

		switch {
		// Tag proposals only come from generation, so a post must wait for the LLM instead of being marked checked without them.
		case errors.Is(err, adapter.ErrPeakHours) || ctx.Err() != nil:
			return nil
		case errors.Is(err, adapter.ErrUnavailable):
			l.Warn("tagging held, generator unavailable", "err", err)
			return nil
		case err != nil:
			l.Warn("generate tags", "post", post.ID, "err", err)
			failed++
		case post.Trusted:
			generated++
		}

		tags, err := s.ListTags(ctx)
		if err != nil {
			return err
		}

		if len(tags) == 0 {
			continue
		}

		keys, err := t.Tags(ctx, pl, post.Title, post.Content, tags)
		aiCalls++

		var gatePassed *bool

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("classify tags", "post", post.ID, "err", err)
			failed++
		// Only trusted sources are judged, so a disabled source collects no results to be flagged on once re-enabled.
		case shadow && post.Trusted:
			gatePassed = shadowGate(v, post.TargetTags, keys)
		}

		if err := s.CompleteTagging(ctx, post.ID, keys, gatePassed); err != nil {
			return err
		}

		processed++
	}

	return nil
}

func generateTags(ctx context.Context, l *slog.Logger, s *Store, g *robot.TagGenerator, v Vocabulary, lang string, post Post) error {
	generated, err := g.Generate(ctx, l, lang, post.Content, v.ActiveKeys())
	if err != nil {
		return fmt.Errorf("err generate tags: %w", err)
	}

	r := route(v, lang, generated)

	if err := s.AddTags(ctx, r.terms); err != nil {
		return fmt.Errorf("err add terms: %w", err)
	}

	if err := s.Reactivate(ctx, r.reactivate); err != nil {
		return fmt.Errorf("err reactivate: %w", err)
	}

	if err := s.AddProposals(ctx, post.ID, r.proposals); err != nil {
		return fmt.Errorf("err add proposals: %w", err)
	}

	return nil
}

func normalizeLang(lang string) string {
	lang, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(lang)), "-")

	return lang
}
