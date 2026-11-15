package domain

import (
	"context"
	"log/slog"
	"testing"
)

type captured struct {
	msg   string
	level slog.Level
	attrs map[string]any
}

type capture struct {
	attrs   []slog.Attr
	records *[]captured
}

func (c capture) Enabled(context.Context, slog.Level) bool { return true }

func (c capture) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any)

	for _, a := range c.attrs {
		attrs[a.Key] = a.Value.Any()
	}

	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})

	*c.records = append(*c.records, captured{msg: r.Message, level: r.Level, attrs: attrs})

	return nil
}

func (c capture) WithAttrs(attrs []slog.Attr) slog.Handler {
	return capture{attrs: append(append([]slog.Attr{}, c.attrs...), attrs...), records: c.records}
}

func (c capture) WithGroup(string) slog.Handler { return c }

func TestLogStep(t *testing.T) {
	cases := map[string]slog.Level{
		OutcomePassed:  slog.LevelInfo,
		OutcomeStopped: slog.LevelWarn,
		OutcomeError:   slog.LevelError,
	}

	for outcome, level := range cases {
		t.Run(outcome, func(t *testing.T) {
			var records []captured

			calls := slog.New(capture{records: &records}).With("raw_post_id", "r1", "step", StepQualityCheck)
			input := map[string]any{"title": "t"}
			output := map[string]any{"score": 0.12}

			LogStep(context.Background(), calls, outcome, "score 0.12, needs 0.80", input, output)

			if len(records) != 1 {
				t.Fatalf("records: %d", len(records))
			}

			r := records[0]

			if r.msg != "step" || r.level != level {
				t.Errorf("msg %q level %v", r.msg, r.level)
			}

			if r.attrs["outcome"] != outcome || r.attrs["reason"] != "score 0.12, needs 0.80" {
				t.Errorf("outcome or reason: %v", r.attrs)
			}

			if r.attrs["request"].(map[string]any)["title"] != "t" || r.attrs["response"].(map[string]any)["score"] != 0.12 {
				t.Errorf("request or response: %v", r.attrs)
			}

			if r.attrs["raw_post_id"] != "r1" || r.attrs["step"] != StepQualityCheck {
				t.Errorf("ids: %v", r.attrs)
			}

			if _, ok := r.attrs["error"]; ok {
				t.Errorf("reason must not fill the error column: %v", r.attrs)
			}
		})
	}
}
