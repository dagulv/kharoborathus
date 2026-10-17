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

func Run(ctx context.Context, s *Store, sw *switches.Store, g *robot.TagGenerator, t *robot.Tagger, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, tickInterval) {
		if err := tagPosts(ctx, s, sw, g, t, l); err != nil {
			l.Error("tag posts", "err", err)
		}
	}

	return ctx.Err()
}

func tagPosts(ctx context.Context, s *Store, sw *switches.Store, g *robot.TagGenerator, t *robot.Tagger, l *slog.Logger) error {
	shadow, err := sw.Enabled(ctx, switches.ShadowGate)
	if err != nil {
		return err
	}

	posts, err := iterx.Collect(s.ListUntaggedPosts(ctx, batchSize))
	if err != nil {
		return err
	}

	for _, post := range posts {
		lang := normalizeLang(post.Lang)

		v, err := s.Vocabulary(ctx)
		if err != nil {
			return fmt.Errorf("err vocabulary: %w", err)
		}

		if err := generateTags(ctx, s, g, v, lang, post); err != nil {
			if !errors.Is(err, adapter.ErrUnavailable) {
				l.Warn("generate tags", "post", post.ID, "err", err)
			}

			if ctx.Err() != nil {
				return nil
			}
		}

		tags, err := s.ListTags(ctx)
		if err != nil {
			return err
		}

		if len(tags) == 0 {
			continue
		}

		keys, err := t.Tags(ctx, post.Title, post.Content, tags)

		var gatePassed *bool

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("classify tags", "post", post.ID, "err", err)
		case shadow:
			gatePassed = shadowGate(v, post.TargetTags, keys)
		}

		if err := s.CompleteTagging(ctx, post.ID, keys, gatePassed); err != nil {
			return err
		}
	}

	return nil
}

func generateTags(ctx context.Context, s *Store, g *robot.TagGenerator, v Vocabulary, lang string, post Post) error {
	generated, err := g.Generate(ctx, lang, post.Content, v.ActiveKeys())
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

	if err := s.AddProposals(ctx, post.ID, post.SourceID, r.proposals); err != nil {
		return fmt.Errorf("err add proposals: %w", err)
	}

	return nil
}

func normalizeLang(lang string) string {
	lang, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(lang)), "-")

	return lang
}
