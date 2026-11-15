package domain

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
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
	// Starts an admin resync. The steps after it are the new run.
	StepResync = "resync"
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

// A step, read back from its logs row.
type Step struct {
	ID        int64           `json:"id"` // the logs row id
	Name      string          `json:"name"`
	Outcome   string          `json:"outcome"`
	Reason    string          `json:"reason"`
	Input     json.RawMessage `json:"input"`
	Output    json.RawMessage `json:"output"`
	CreatedAt time.Time       `json:"createdAt"`
}

type TraceStep struct {
	Step

	Calls []LogSummary `json:"calls"`
	// The domain-level call that made the extract rule this step used. Extract only.
	RuleCall *LogSummary `json:"ruleCall,omitempty"`
}

type Trace struct {
	Path  string      `json:"path"` // sourced | discovered
	Steps []TraceStep `json:"steps"`
}

// BuildTrace turns one raw post's logs rows, in id order, into its steps. A call belongs to the
// next step row with the same step name, which tells retried attempts apart. A call whose step
// has not logged yet gets a pending step. byStep calls go to the last step of their name.
func BuildTrace(rows []LogDetail, byStep map[string][]LogSummary) []TraceStep {
	steps := []TraceStep{}
	waiting := map[string][]LogSummary{}

	for _, row := range rows {
		name, _ := row.Metadata["step"].(string)

		if row.Type != "step" {
			waiting[name] = append(waiting[name], row.LogSummary)
			continue
		}

		outcome, _ := row.Metadata["outcome"].(string)
		reason, _ := row.Metadata["reason"].(string)

		steps = append(steps, TraceStep{
			Step:  Step{ID: row.ID, Name: name, Outcome: outcome, Reason: reason, Input: row.Request, Output: row.Response, CreatedAt: row.CreatedAt},
			Calls: append([]LogSummary{}, waiting[name]...),
		})

		delete(waiting, name)
	}

	for _, row := range rows {
		name, _ := row.Metadata["step"].(string)

		calls, ok := waiting[name]
		if !ok {
			continue
		}

		steps = append(steps, TraceStep{
			Step:  Step{ID: calls[0].ID, Name: name, Outcome: OutcomePending, CreatedAt: calls[0].CreatedAt},
			Calls: calls,
		})

		delete(waiting, name)
	}

	for name, calls := range byStep {
		for i := len(steps) - 1; i >= 0; i-- {
			if steps[i].Name == name {
				steps[i].Calls = append(steps[i].Calls, calls...)
				break
			}
		}
	}

	return steps
}
