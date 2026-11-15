package domain

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"testing"
	"time"
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

func TestBuildTrace(t *testing.T) {
	at := time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC)

	call := func(id int64, typ, step string) LogDetail {
		return LogDetail{
			LogSummary: LogSummary{ID: id, Type: typ, Metadata: map[string]any{"raw_post_id": "r", "step": step}, CreatedAt: at.Add(time.Duration(id) * time.Second)},
			Request:    json.RawMessage(`{"big":"body"}`),
			Response:   json.RawMessage(`{"big":"body"}`),
		}
	}

	step := func(id int64, name, outcome, reason, input, output string) LogDetail {
		return LogDetail{
			LogSummary: LogSummary{ID: id, Type: "step", Metadata: map[string]any{"raw_post_id": "r", "step": name, "outcome": outcome, "reason": reason}, CreatedAt: at.Add(time.Duration(id) * time.Second)},
			Request:    json.RawMessage(input),
			Response:   json.RawMessage(output),
		}
	}

	rows := []LogDetail{
		step(1, StepExtract, OutcomePassed, "", `{"bytes":10}`, `{"title":"t"}`),
		call(2, "jev", StepQualityCheck),
		step(3, StepQualityCheck, OutcomeError, "jev down", `{}`, `null`),
		call(4, "jev", StepQualityCheck),
		step(5, StepQualityCheck, OutcomeStopped, "score 0.12, needs 0.80", `{}`, `{"score":0.12}`),
		call(6, "deepseek", StepEnrich),
	}

	brave := LogSummary{ID: 99, Type: "brave"}
	got := BuildTrace(rows, map[string][]LogSummary{StepQualityCheck: {brave}, StepDiscover: {brave}})

	var names []string
	for _, s := range got {
		names = append(names, s.Name+":"+s.Outcome)
	}

	if want := []string{"extract:passed", "quality_check:error", "quality_check:stopped", "enrich:pending"}; !slices.Equal(names, want) {
		t.Fatalf("steps %v, want %v", names, want)
	}

	ids := func(calls []LogSummary) []int64 {
		out := []int64{}
		for _, c := range calls {
			out = append(out, c.ID)
		}
		return out
	}

	if got[0].Calls == nil || len(got[0].Calls) != 0 || string(got[0].Input) != `{"bytes":10}` || string(got[0].Output) != `{"title":"t"}` {
		t.Errorf("extract %+v", got[0])
	}

	if !slices.Equal(ids(got[1].Calls), []int64{2}) || got[1].Reason != "jev down" {
		t.Errorf("first attempt %+v", got[1])
	}

	if !slices.Equal(ids(got[2].Calls), []int64{4, 99}) || got[2].Reason != "score 0.12, needs 0.80" || got[2].ID != 5 {
		t.Errorf("second attempt %+v", got[2])
	}

	if !slices.Equal(ids(got[3].Calls), []int64{6}) {
		t.Errorf("pending enrich %+v", got[3])
	}
}
