package tagging

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/iterx"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	tickInterval = time.Minute
	batchSize    = 20
	englishLang  = "en"
)

func Run(ctx context.Context, s *Store, g *robot.TagGenerator, t *robot.Tagger, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, tickInterval) {
		if err := tagPosts(ctx, s, g, t, l); err != nil {
			l.Error("tag posts", "err", err)
		}
	}

	return ctx.Err()
}

func tagPosts(ctx context.Context, s *Store, g *robot.TagGenerator, t *robot.Tagger, l *slog.Logger) error {
	posts, err := iterx.Collect(s.ListUntaggedPosts(ctx, batchSize))
	if err != nil {
		return err
	}

	for _, post := range posts {
		lang := normalizeLang(post.Lang)

		if err := generateTags(ctx, s, g, lang, post); err != nil {
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

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("classify tags", "post", post.ID, "err", err)
		}

		if err := s.CompleteTagging(ctx, post.ID, keys); err != nil {
			return err
		}
	}

	return nil
}

func generateTags(ctx context.Context, s *Store, g *robot.TagGenerator, lang string, post Post) error {
	tags, err := s.ListTags(ctx)
	if err != nil {
		return err
	}

	existing := make([]string, len(tags))
	for i, tag := range tags {
		existing[i] = tag.Key
	}

	generated, err := g.Generate(ctx, lang, post.Content, existing)
	if err != nil {
		return err
	}

	terms := make([]Term, 0, 2*len(generated))

	for _, tag := range generated {
		terms = append(terms, Term{Key: tag.Key, Lang: englishLang, Term: strings.ReplaceAll(tag.Key, "_", " ")})

		if lang != "" && lang != englishLang {
			terms = append(terms, Term{Key: tag.Key, Lang: lang, Term: tag.Term})
		}
	}

	return s.AddTags(ctx, terms)
}

func normalizeLang(lang string) string {
	lang, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(lang)), "-")

	return lang
}
