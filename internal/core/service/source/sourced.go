package source

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

const (
	DomainPace = time.Second * 5
	workers    = 5
)

// Keyed by domain.Source.Adapter. Backfills get their own lane per adapter, so a long archive never
// holds up live sources. Third-party requests go to calls, one row each in the logs table.
func Run(ctx context.Context, s *Service, fetchers map[string]port.Fetcher, backfills <-chan domain.Source, l, calls *slog.Logger) error {
	ins := make(map[string]chan domain.Source, len(fetchers))
	backIns := make(map[string]chan domain.Source, len(fetchers))
	posts := make([]<-chan domain.RawPost, 0, 2*len(fetchers))

	for name, f := range fetchers {
		in := make(chan domain.Source)
		ins[name] = in
		posts = append(posts, lane(ctx, l.With("adapter", name), calls, in, f.Fetch))

		b, ok := f.(port.Backfiller)
		if !ok {
			continue
		}

		backIn := make(chan domain.Source)
		backIns[name] = backIn
		posts = append(posts, lane(ctx, l.With("adapter", name, "backfill", true), calls, backIn, b.Backfill))
	}

	go route(ctx, s.sourceStream(ctx, l.WithGroup("sourceStream")), ins, l, "unknown adapter")
	go route(ctx, backfills, backIns, l, "no backfill strategy")
	go s.retryPending(ctx, l.WithGroup("retryPending"))

	return pipeline.New[domain.RawPost]().
		Next(workers, s.claim).
		Next(workers, s.fetch).
		Next(workers, s.Extract).
		Next(workers, SetMarkdown).
		Next(workers, SetSimhash).
		Next(workers, s.saveContent).
		Next(workers, s.checkQuality).
		WithSink(func(ctx context.Context, p domain.RawPost) error { return s.saveStatus(ctx, l, p) }).
		Run(ctx, l, pipeline.Merge(ctx, posts...))
}

// lane reads sources, workers at a time, and gives each post its source and language.
func lane(ctx context.Context, l, calls *slog.Logger, sources <-chan domain.Source, read func(context.Context, *slog.Logger, string) (<-chan domain.RawPost, error)) <-chan domain.RawPost {
	return pipeline.New[domain.Source]().
		NextCh(workers, func(ctx context.Context, l *slog.Logger, in <-chan domain.Source) <-chan domain.RawPost {
			out := make(chan domain.RawPost)

			go func() {
				defer close(out)

				for source := range in {
					posts, err := read(ctx, calls.With("source_id", source.ID), source.URL)
					if errors.Is(err, port.ErrNoArchive) {
						l.Info("no archive", "source", source.ID, "url", source.URL, "err", err)
						continue
					}

					if err != nil {
						l.Error("read source", "source", source.ID, "url", source.URL, "err", err)
						continue
					}

					for p := range posts {
						p.SourceID = source.ID
						p.Lang = cmp.Or(normalizeLang(p.Lang), source.Lang)

						select {
						case out <- p:
						case <-ctx.Done():
							return
						}
					}
				}
			}()

			return out
		}).
		Stream(ctx, l, sources)
}

// Closing every input ends each plugin's stream, which ends the merge and so Run. The backfill channel is never closed, so ctx ends it.
func route(ctx context.Context, sources <-chan domain.Source, ins map[string]chan domain.Source, l *slog.Logger, missing string) {
	defer func() {
		for _, c := range ins {
			close(c)
		}
	}()

	for {
		var source domain.Source

		select {
		case s, ok := <-sources:
			if !ok {
				return
			}

			source = s
		case <-ctx.Done():
			return
		}

		c, ok := ins[source.Adapter]
		if !ok {
			l.Error(missing, "adapter", source.Adapter, "source", source.ID)
			continue
		}

		select {
		case c <- source:
		case <-ctx.Done():
			return
		}
	}
}

// Claimed before the fetch, so every step and call logs against the raw post. A url another
// sync owns, or one already done, is skipped.
func (s *Service) claim(ctx context.Context, _ *slog.Logger, p domain.RawPost) (domain.RawPost, error) {
	claimed, ok, err := s.RawPostStore.ClaimRawPost(ctx, postgres.Claim{URL: p.URL, Lang: p.Lang, SourceID: p.SourceID})
	if err != nil {
		return p, err
	}

	if !ok {
		return p, pipeline.ErrSkip
	}

	p.ID, p.DomainID, p.CreatedAt, p.Status = claimed.ID, claimed.DomainID, claimed.CreatedAt, claimed.Status

	return p, nil
}

// A feed item keeps its feed content when its article fails to fetch.
func (s *Service) fetch(ctx context.Context, _ *slog.Logger, p domain.RawPost) (domain.RawPost, error) {
	page, finalURL, err := s.Pages.Page(ctx, s.callsFor(p.ID, domain.StepFetch), p.URL)
	if ctx.Err() != nil {
		return p, ctx.Err()
	}

	input := map[string]any{"url": p.URL}
	output := map[string]any{"bytes": len(page), "finalUrl": finalURL, "feedChars": len(p.Content)}

	if err != nil && p.Content == "" {
		return p, s.stop(ctx, p, domain.StatusFailed, domain.StepFetch, "fetch failed: "+err.Error(), input, output)
	}

	reason := ""
	if err != nil {
		reason = "article fetch failed, kept feed content: " + err.Error()
	}

	p.RawContent, p.FinalURL = string(page), finalURL
	s.step(ctx, p.ID, domain.StepFetch, domain.OutcomePassed, reason, input, output)

	return p, nil
}

func (s *Service) saveContent(ctx context.Context, _ *slog.Logger, p domain.RawPost) (domain.RawPost, error) {
	return s.RawPostStore.SaveContent(ctx, p)
}

// A dropped id is found again by enrich's catch-up job, so a full queue is not a failure.
func (s *Service) saveStatus(ctx context.Context, l *slog.Logger, p domain.RawPost) error {
	if err := s.RawPostStore.SetStatus(ctx, p.ID, p.Status); err != nil || p.Status != domain.StatusAccepted {
		return err
	}

	select {
	case s.Enriched <- p.ID:
	default:
		l.Warn("enrich queue full", "raw_post", p.ID)
	}

	return nil
}
