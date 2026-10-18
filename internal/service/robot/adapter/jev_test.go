package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testJev(t *testing.T, handler http.HandlerFunc) jev {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return jev{client: srv.Client(), url: srv.URL, key: "secret", model: JevModel, l: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestJevNoul(t *testing.T) {
	j := testJev(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request %s %q", r.Method, r.Header.Get("Authorization"))
		}

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}

		q := req["questions"].(map[string]any)["tag_0"].(map[string]any)
		if req["model"] != JevModel || q["type"] != "noul" || q["instructions"] != "Is it about investments?" || req["state"].(map[string]any)["title"] != "Volvo" {
			t.Errorf("body %v", req)
		}

		if _, ok := q["criteria"]; ok {
			t.Error("empty criteria sent")
		}

		w.Write([]byte(`{"model":"jev-1.14.0","answers":{"tag_0":{"type":"noul","noul":0.93},"tag_1":{"type":"noul","noul":0.04}},"usage":{"input_tokens":360,"output_tokens":39}}`))
	})

	got, err := j.Noul(context.Background(), slog.New(slog.DiscardHandler), map[string]string{"title": "Volvo"}, map[string]NoulQuestion{
		"tag_0": {Instructions: "Is it about investments?"},
		"tag_1": {Instructions: "Is it about sports?", Criteria: &NoulCriteria{True: "a sport", False: "no sport"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got["tag_0"] != 0.93 || got["tag_1"] != 0.04 {
		t.Errorf("answers %v", got)
	}
}

func TestJevErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   error
	}{
		"rate limited": {http.StatusTooManyRequests, "", ErrUnavailable},
		"overloaded":   {statusJevOverloaded, "", ErrUnavailable},
		"missing":      {http.StatusOK, `{"answers":{}}`, ErrIncomplete},
	}

	for name, c := range cases {
		j := testJev(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		})

		if _, err := j.Noul(context.Background(), slog.New(slog.DiscardHandler), "x", map[string]NoulQuestion{"a": {Instructions: "?"}}); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v", name, err)
		}
	}

	j := testJev(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"detail":"bad question"}`))
	})

	if _, err := j.Noul(context.Background(), slog.New(slog.DiscardHandler), "x", map[string]NoulQuestion{"a": {Instructions: "?"}}); err == nil || errors.Is(err, ErrUnavailable) {
		t.Errorf("422: got %v", err)
	}
}
