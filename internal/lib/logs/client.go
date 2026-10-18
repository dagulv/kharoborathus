package logs

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Client returns a copy of c that logs each request to l under typ. Headers are never
// logged, since they carry the API keys. Build it per call, so l can carry that call's ids.
func Client(c *http.Client, l *slog.Logger, typ string) *http.Client {
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}

	out := *c
	out.Transport = transport{base: base, l: l, typ: typ}

	return &out
}

type transport struct {
	base http.RoundTripper
	l    *slog.Logger
	typ  string
}

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()

	request := map[string]any{"method": req.Method, "url": req.URL.String()}

	// Only GetBody gives a copy. Reading Body itself would consume what the server must get.
	if req.GetBody != nil {
		if rc, err := req.GetBody(); err == nil {
			b, _ := io.ReadAll(rc)
			rc.Close()

			if len(b) > 0 {
				request["body"] = body(b)
			}
		}
	}

	res, err := t.base.RoundTrip(req)
	if err != nil {
		t.l.LogAttrs(req.Context(), slog.LevelError, t.typ,
			slog.Any("request", request),
			slog.Duration("duration", time.Since(start)),
			slog.Any("error", err),
		)

		return nil, err
	}

	level := slog.LevelInfo
	if res.StatusCode >= http.StatusBadRequest {
		level = slog.LevelWarn
	}

	// The response is logged when the caller closes the body, holding as much as the caller read.
	lb := &loggedBody{ReadCloser: res.Body}
	lb.done = func() {
		attrs := []slog.Attr{
			slog.Any("request", request),
			slog.Int("status", res.StatusCode),
			slog.Duration("duration", time.Since(start)),
		}

		if lb.buf.Len() > 0 {
			attrs = append(attrs, slog.Any("response", body(lb.buf.Bytes())))
		}

		if lb.err != nil {
			attrs = append(attrs, slog.Any("error", lb.err))
		}

		t.l.LogAttrs(req.Context(), level, t.typ, attrs...)
	}

	res.Body = lb

	return res, nil
}

type loggedBody struct {
	io.ReadCloser
	buf  bytes.Buffer
	err  error
	once sync.Once
	done func()
}

func (b *loggedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.buf.Write(p[:n])

	if err != nil && err != io.EOF {
		b.err = err
	}

	return n, err
}

func (b *loggedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.done)

	return err
}

// JSON bodies are stored as JSON, so they can be queried. Anything else is stored as a string.
func body(b []byte) any {
	if json.Valid(b) {
		return json.RawMessage(b)
	}

	return string(b)
}
