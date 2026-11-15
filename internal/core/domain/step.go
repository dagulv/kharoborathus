package domain

import (
	"context"
	"log/slog"
)

// Pipeline steps a raw post passes through, in the order of either path.
const (
	StepFoundBy        = "found_by"
	StepDomainCheck    = "domain_check"
	StepFetch          = "fetch"
	StepExtract        = "extract"
	StepDateWindow     = "date_window"
	StepSourceMatch    = "source_match"
	StepDuplicateCheck = "duplicate_check"
	StepQualityCheck   = "quality_check"
	StepEnrich         = "enrich"
	StepDiscover       = "discover"
)

const (
	OutcomePassed  = "passed"
	OutcomeStopped = "stopped"
	OutcomeError   = "error"
	// Only in a trace, for calls whose step has not logged yet.
	OutcomePending = "pending"
)

// LogStep writes one logs row of type step. calls carries raw_post_id and step.
// The reason goes in metadata, since the error column is for failed requests.
func LogStep(ctx context.Context, calls *slog.Logger, outcome, reason string, input, output any) {
	level := slog.LevelInfo

	switch outcome {
	case OutcomeStopped:
		level = slog.LevelWarn
	case OutcomeError:
		level = slog.LevelError
	}

	calls.LogAttrs(ctx, level, "step",
		slog.String("outcome", outcome),
		slog.String("reason", reason),
		slog.Any("request", input),
		slog.Any("response", output),
	)
}
