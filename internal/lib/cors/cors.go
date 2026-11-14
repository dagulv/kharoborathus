// Package cors lets the admin SPA, on its own origin, call the API with the session cookie.
package cors

import (
	"net/http"
	"slices"
)

// Requests from other origins still reach next. Without the headers the browser keeps their answers from the page.
func Allow(origins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := origin != "" && slices.Contains(origins, origin)

		if allowed {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Add("Vary", "Origin")
		}

		if r.Method != http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		if allowed {
			h := w.Header()
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH")
			h.Set("Access-Control-Allow-Headers", "Content-Type")
			h.Set("Access-Control-Max-Age", "600")
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
