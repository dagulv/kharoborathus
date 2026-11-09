package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeSessions struct {
	valid []byte
	err   error
	got   []byte
}

func (f *fakeSessions) ValidSession(_ context.Context, tokenHash []byte) (bool, error) {
	f.got = tokenHash
	return bytes.Equal(tokenHash, f.valid), f.err
}

func TestRequireSession(t *testing.T) {
	hash := sha256.Sum256([]byte("good-token"))
	sessions := &fakeSessions{valid: hash[:]}

	called := false
	h := RequireSession(sessions, slog.New(slog.DiscardHandler), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	cases := map[string]struct {
		header string
		want   int
		called bool
	}{
		"valid":      {"Bearer good-token", http.StatusOK, true},
		"no header":  {"", http.StatusUnauthorized, false},
		"wrong kind": {"Basic good-token", http.StatusUnauthorized, false},
		"empty":      {"Bearer ", http.StatusUnauthorized, false},
		"invalid":    {"Bearer bad-token", http.StatusUnauthorized, false},
	}

	for name, c := range cases {
		called = false

		req := httptest.NewRequest(http.MethodGet, "/admin/listing", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != c.want || called != c.called {
			t.Errorf("%s: status %d, called %v", name, rec.Code, called)
		}
	}
}

func TestRequireSessionStoreError(t *testing.T) {
	h := RequireSession(&fakeSessions{err: errors.New("db down")}, slog.New(slog.DiscardHandler), http.NotFoundHandler())

	req := httptest.NewRequest(http.MethodGet, "/admin/listing", nil)
	req.Header.Set("Authorization", "Bearer t")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d", rec.Code)
	}
}
