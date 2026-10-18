// Package logs writes slog records to the logs table, one row per outbound request.
package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/database"
)

// Handler inserts one row per record, following the slog handler guide. The message is the
// row's type. The record's request, response, status, duration and error attrs fill their
// columns, even inside a group. Every other attr, and those from With and WithGroup, nest
// into metadata. Inserts are synchronous, so a slow database slows the logged call.
type Handler struct {
	db       *database.Database
	fallback *slog.Logger
	goas     []groupOrAttrs
}

type groupOrAttrs struct {
	group string
	attrs []slog.Attr
}

func NewHandler(db *database.Database, fallback *slog.Logger) *Handler {
	return &Handler{db: db, fallback: fallback}
}

func (h *Handler) Enabled(context.Context, slog.Level) bool { return true }

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	row := h.row(r)

	// A call cut short by its context is still logged.
	_, err := h.db.Exec(context.WithoutCancel(ctx), `
		INSERT INTO logs (type, level, request, response, status, duration_ms, error, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, coalesce($9, now()))
	`, row.Type, row.Level, row.Request, row.Response, row.Status, row.DurationMs, row.Error, jsonText(row.Meta), row.Time)
	if err != nil {
		h.fallback.Error("insert log", "type", row.Type, "err", err)
		return fmt.Errorf("err insert log: %w", err)
	}

	return nil
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	return h.with(groupOrAttrs{attrs: attrs})
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return h.with(groupOrAttrs{group: name})
}

func (h *Handler) with(g groupOrAttrs) *Handler {
	out := *h
	out.goas = append(slices.Clip(h.goas), g)

	return &out
}

type row struct {
	Type       string
	Level      string
	Time       *time.Time
	Request    string
	Response   *string
	Status     *int
	DurationMs int64
	Error      *string
	Meta       map[string]any
}

func (h *Handler) row(r slog.Record) row {
	out := row{Type: r.Message, Level: r.Level.String(), Request: "{}", Meta: make(map[string]any)}

	if !r.Time.IsZero() {
		out.Time = &r.Time
	}

	cur := out.Meta

	for _, g := range h.goas {
		if g.group == "" {
			addAttrs(cur, g.attrs)
			continue
		}

		next := make(map[string]any)
		cur[g.group] = next
		cur = next
	}

	var rest []slog.Attr

	r.Attrs(func(a slog.Attr) bool {
		v := a.Value.Resolve()

		switch a.Key {
		case "request":
			out.Request = jsonText(value(v))
		case "response":
			s := jsonText(value(v))
			out.Response = &s
		case "status":
			n := int(v.Int64())
			out.Status = &n
		case "duration":
			out.DurationMs = v.Duration().Milliseconds()
		case "error":
			s := fmt.Sprint(v.Any())
			out.Error = &s
		default:
			rest = append(rest, a)
		}

		return true
	})

	addAttrs(cur, rest)
	prune(out.Meta)

	return out
}

func addAttrs(m map[string]any, attrs []slog.Attr) {
	for _, a := range attrs {
		a.Value = a.Value.Resolve()

		if a.Equal(slog.Attr{}) {
			continue
		}

		if a.Value.Kind() == slog.KindGroup && a.Key == "" {
			addAttrs(m, a.Value.Group())
			continue
		}

		m[a.Key] = value(a.Value)
	}
}

func value(v slog.Value) any {
	v = v.Resolve()

	switch v.Kind() {
	case slog.KindGroup:
		m := make(map[string]any)
		addAttrs(m, v.Group())
		return m
	case slog.KindAny:
		// json.Marshal turns most errors into {}.
		if err, ok := v.Any().(error); ok {
			return err.Error()
		}
	}

	return v.Any()
}

// Groups without attrs are dropped, as slog requires.
func prune(m map[string]any) {
	for k, v := range m {
		sub, ok := v.(map[string]any)
		if !ok {
			continue
		}

		prune(sub)

		if len(sub) == 0 {
			delete(m, k)
		}
	}
}

// Postgres rejects NUL in jsonb, and fetched pages sometimes hold one.
// HTML stays unescaped, so stored pages read as HTML.
func jsonText(v any) string {
	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		b.Reset()
		enc.Encode(fmt.Sprint(v))
	}

	return string(bytes.ReplaceAll(bytes.TrimSpace(b.Bytes()), []byte(`\u0000`), nil))
}
