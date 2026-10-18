package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/slogtest"
	"time"
)

// recorder builds rows like Handler, without a database.
type recorder struct {
	*Handler
	rows *[]row
}

func newRecorder() recorder {
	return recorder{Handler: &Handler{}, rows: new([]row)}
}

func (r recorder) Handle(_ context.Context, rec slog.Record) error {
	*r.rows = append(*r.rows, r.row(rec))
	return nil
}

func (r recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	return recorder{Handler: r.Handler.WithAttrs(attrs).(*Handler), rows: r.rows}
}

func (r recorder) WithGroup(name string) slog.Handler {
	return recorder{Handler: r.Handler.WithGroup(name).(*Handler), rows: r.rows}
}

func TestHandlerFollowsSlog(t *testing.T) {
	rec := newRecorder()

	results := func() []map[string]any {
		var out []map[string]any

		for _, r := range *rec.rows {
			m := map[string]any{slog.MessageKey: r.Type, slog.LevelKey: r.Level}
			if r.Time != nil {
				m[slog.TimeKey] = *r.Time
			}

			for k, v := range r.Meta {
				m[k] = v
			}

			out = append(out, m)
		}

		return out
	}

	if err := slogtest.TestHandler(rec, results); err != nil {
		t.Error(err)
	}
}

func TestRowColumns(t *testing.T) {
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "page", 0)
	r.AddAttrs(
		slog.Any("request", map[string]any{"url": "https://x.example/"}),
		slog.Any("response", "<p>a\x00b\xff</p>"),
		slog.Int("status", 404),
		slog.Duration("duration", 1500*time.Millisecond),
		slog.Any("error", errors.New("boom")),
		slog.Any("cause", errors.New("inner")),
	)

	h := (&Handler{}).WithAttrs([]slog.Attr{slog.String("source_id", "s1")}).WithGroup("post").WithAttrs([]slog.Attr{slog.String("id", "p1")})
	got := h.(*Handler).row(r)

	if got.Type != "page" || got.Level != "WARN" || *got.Status != 404 || got.DurationMs != 1500 || *got.Error != "boom" {
		t.Errorf("row %+v", got)
	}

	if *got.Response != `"<p>ab�</p>"` || got.Request != `{"url":"https://x.example/"}` {
		t.Errorf("request %s, response %s", got.Request, *got.Response)
	}

	if meta := jsonText(got.Meta); meta != `{"post":{"cause":"inner","id":"p1"},"source_id":"s1"}` {
		t.Errorf("metadata %s", meta)
	}
}

func TestClientLogsRequestAndResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, _ := io.ReadAll(r.Body); string(b) != `{"q":"hej"}` {
			t.Errorf("server got body %q", b)
		}

		w.Write([]byte(`{"answer":1}`))
	}))
	defer srv.Close()

	rec := newRecorder()
	client := Client(srv.Client(), slog.New(rec).With("post_id", "p1"), "jev")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/x", bytes.NewReader([]byte(`{"q":"hej"}`)))
	req.Header.Set("Authorization", "Bearer secret")

	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := io.ReadAll(res.Body); string(b) != `{"answer":1}` {
		t.Errorf("caller got body %q", b)
	}

	if len(*rec.rows) != 0 {
		t.Error("logged before the body was closed")
	}

	res.Body.Close()
	res.Body.Close()

	if len(*rec.rows) != 1 {
		t.Fatalf("%d rows", len(*rec.rows))
	}

	r := (*rec.rows)[0]

	if r.Type != "jev" || *r.Status != 200 || r.Error != nil || r.Meta["post_id"] != "p1" {
		t.Errorf("row %+v", r)
	}

	if !strings.Contains(r.Request, `"body":{"q":"hej"}`) || !strings.Contains(r.Request, `"method":"POST"`) || strings.Contains(r.Request, "secret") {
		t.Errorf("request %s", r.Request)
	}

	var body map[string]int
	if err := json.Unmarshal([]byte(*r.Response), &body); err != nil || body["answer"] != 1 {
		t.Errorf("response %s", *r.Response)
	}
}

func TestClientLogsFailure(t *testing.T) {
	rec := newRecorder()

	if _, err := Client(&http.Client{}, slog.New(rec), "brave").Get("http://127.0.0.1:1/"); err == nil {
		t.Fatal("no error")
	}

	if rows := *rec.rows; len(rows) != 1 || rows[0].Error == nil || rows[0].Status != nil || rows[0].Response != nil || rows[0].Level != "ERROR" {
		t.Fatalf("rows %+v", rows)
	}
}
