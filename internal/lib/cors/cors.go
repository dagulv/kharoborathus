// Package cors lets the admin SPA, on its own origin, call the API with the session cookie.
package cors

import (
	"net/http"
	"slices"
)

// Any other origin is refused, not just left without headers. Sibling subdomains are same-site,
// so the Strict cookie goes along and a GET would otherwise still run with it.
// Requests without an Origin, like curl or a same-origin GET, pass.
func Allow(origins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		if !slices.Contains(origins, origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}

		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Add("Vary", "Origin")

		if r.Method != http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
	})
}
