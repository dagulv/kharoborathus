package source

import (
	"context"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
)

func (s *Service) callsFor(rawPostID, step string) *slog.Logger {
	return s.Calls.With("raw_post_id", rawPostID, "step", step)
}

func (s *Service) step(ctx context.Context, rawPostID, name, outcome, reason string, input, output any) {
	domain.LogStep(ctx, s.callsFor(rawPostID, name), outcome, reason, input, output)
}

// stop ends the post at this step with its final status.
func (s *Service) stop(ctx context.Context, p domain.RawPost, status, name, reason string, input, output any) error {
	s.step(ctx, p.ID, name, domain.OutcomeStopped, reason, input, output)

	if err := s.RawPostStore.SetStatus(ctx, p.ID, status); err != nil {
		return err
	}

	return pipeline.ErrSkip
}
