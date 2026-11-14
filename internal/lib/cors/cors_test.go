package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllow(t *testing.T) {
	called := false
	h := Allow([]string{"http://localhost:5173"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	cases := []struct {
		name, method, origin string
		wantStatus           int
		wantACAO             string
		wantCalled           bool
	}{
		{"allowed get", http.MethodGet, "http://localhost:5173", http.StatusOK, "http://localhost:5173", true},
		{"other get", http.MethodGet, "https://evil.example", http.StatusOK, "", true},
		{"no origin", http.MethodGet, "", http.StatusOK, "", true},
		{"allowed preflight", http.MethodOptions, "http://localhost:5173", http.StatusNoContent, "http://localhost:5173", false},
		{"other preflight", http.MethodOptions, "https://evil.example", http.StatusNoContent, "", false},
	}

	for _, c := range cases {
		called = false

		req := httptest.NewRequest(c.method, "/admin/stats", nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		hdr := rec.Header()

		if rec.Code != c.wantStatus || called != c.wantCalled || hdr.Get("Access-Control-Allow-Origin") != c.wantACAO {
			t.Errorf("%s: status %d, called %v, acao %q", c.name, rec.Code, called, hdr.Get("Access-Control-Allow-Origin"))
		}

		if c.wantACAO == "" {
			if hdr.Get("Access-Control-Allow-Credentials") != "" || hdr.Get("Access-Control-Allow-Methods") != "" {
				t.Errorf("%s: CORS headers for a disallowed origin: %v", c.name, hdr)
			}

			continue
		}

		if hdr.Get("Access-Control-Allow-Credentials") != "true" || hdr.Get("Vary") != "Origin" {
			t.Errorf("%s: headers %v", c.name, hdr)
		}

		if c.method == http.MethodOptions && (hdr.Get("Access-Control-Allow-Methods") != "GET, POST, PATCH" || hdr.Get("Access-Control-Allow-Headers") != "Content-Type" || hdr.Get("Access-Control-Max-Age") != "600") {
			t.Errorf("%s: preflight headers %v", c.name, hdr)
		}
	}
}
