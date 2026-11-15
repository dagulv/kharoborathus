package source

import (
	"context"
	"errors"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

const ResyncQueue = 20

// RunResyncs takes the posts an admin resyncs, one at a time.
func RunResyncs(ctx context.Context, d Discovered, resyncs <-chan domain.Resync, l *slog.Logger) error {
	for {
		select {
		case r := <-resyncs:
			if err := d.Resync(ctx, l, r); err != nil && ctx.Err() == nil {
				l.Error("resync", "raw_post", r.RawPostID, "err", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Resync runs a raw post through the path it first took again, from its fetch. The old content
// is dropped, so extraction starts over, but the title, authors and date stay for extraction to
// fill around, as a feed's would.
func (d Discovered) Resync(ctx context.Context, l *slog.Logger, r domain.Resync) error {
	s := d.Service

	p, ok, err := s.RawPostStore.Reclaim(ctx, r.RawPostID)
	if err != nil {
		return err
	}

	if !ok {
		l.Info("resync skipped, unknown or still in the pipeline", "raw_post", r.RawPostID)
		return nil
	}

	p.Content, p.RawContent, p.Simhash, p.Override = "", "", nil, r.Override
	l = l.With("raw_post", p.ID, "url", p.URL)

	s.step(ctx, p.ID, domain.StepResync, domain.OutcomePassed, "", map[string]any{"override": r.Override}, nil)

	if p.DiscoveryPostID != "" {
		fromPostID, err := d.Store.FromPost(ctx, p.DiscoveryPostID)
		if err != nil {
			return err
		}

		var status string

		if p, status, err = d.process(ctx, l, p, fromPostID); err != nil || status != domain.StatusAccepted {
			return err
		}
	} else {
		// The sourced pipeline's stages after its claim.
		for _, stage := range []func(context.Context, *slog.Logger, domain.RawPost) (domain.RawPost, error){s.fetch, s.Extract, SetMarkdown, SetSimhash, s.saveContent} {
			if p, err = stage(ctx, l, p); err != nil {
				break
			}
		}

		if errors.Is(err, pipeline.ErrSkip) {
			return nil
		}

		if err != nil {
			return err
		}
	}

	p, err = s.checkQuality(ctx, l, p)
	if errors.Is(err, pipeline.ErrSkip) {
		return nil
	}

	if err != nil {
		return err
	}

	return s.saveStatus(ctx, l, p)
}
