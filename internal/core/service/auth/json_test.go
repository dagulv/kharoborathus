package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireJSON(t *testing.T) {
	h := RequireJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	cases := []struct {
		method, contentType string
		want                int
	}{
		{http.MethodGet, "", http.StatusOK},
		{http.MethodPost, "application/json; charset=utf-8", http.StatusOK},
		{http.MethodPatch, "application/json", http.StatusOK},
		{http.MethodPost, "", http.StatusUnsupportedMediaType},
		{http.MethodPost, "text/plain", http.StatusUnsupportedMediaType},
		{http.MethodPost, "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{http.MethodPatch, "text/plain", http.StatusUnsupportedMediaType},
	}

	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/admin/x", nil)
		if c.contentType != "" {
			req.Header.Set("Content-Type", c.contentType)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != c.want {
			t.Errorf("%s %q: %d, want %d", c.method, c.contentType, rec.Code, c.want)
		}
	}
}
