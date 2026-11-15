package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

type record struct {
	msg   string
	level slog.Level
	attrs map[string]any
}

// capture keeps every record logged through calls, With attrs included.
type capture struct {
	mu      *sync.Mutex
	attrs   []slog.Attr
	records *[]record
}

func newCapture() capture {
	return capture{mu: &sync.Mutex{}, records: new([]record)}
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

	c.mu.Lock()
	defer c.mu.Unlock()

	*c.records = append(*c.records, record{msg: r.Message, level: r.Level, attrs: attrs})

	return nil
}

func (c capture) WithAttrs(attrs []slog.Attr) slog.Handler {
	return capture{mu: c.mu, attrs: append(append([]slog.Attr{}, c.attrs...), attrs...), records: c.records}
}

func (c capture) WithGroup(string) slog.Handler { return c }

// steps returns the step records of one raw post.
func (c capture) steps(rawPostID string) []record {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []record

	for _, r := range *c.records {
		if r.msg == "step" && r.attrs["raw_post_id"] == rawPostID {
			out = append(out, r)
		}
	}

	return out
}

// stepLine is "name:outcome" for each step, joined with spaces.
func (c capture) stepLine(rawPostID string) string {
	var parts []string

	for _, r := range c.steps(rawPostID) {
		parts = append(parts, fmt.Sprintf("%s:%s", r.attrs["step"], r.attrs["outcome"]))
	}

	return strings.Join(parts, " ")
}

func (c capture) last(rawPostID string) record {
	steps := c.steps(rawPostID)
	if len(steps) == 0 {
		return record{attrs: map[string]any{}}
	}

	return steps[len(steps)-1]
}

// failing stands in for a logs table that rejects every insert.
type failing struct{}

func (failing) Enabled(context.Context, slog.Level) bool  { return true }
func (failing) Handle(context.Context, slog.Record) error { return errors.New("insert failed") }
func (f failing) WithAttrs([]slog.Attr) slog.Handler      { return f }
func (f failing) WithGroup(string) slog.Handler           { return f }
