package auth

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"net/http"
	"strings"
)

type Sessions interface {
	ValidSession(ctx context.Context, tokenHash []byte) (bool, error)
}

// The web app forwards the admin's session token as a bearer token. Sessions store only its sha256,
// hashed from the token string as the web app's login does.
func RequireSession(s Sessions, l *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		hash := sha256.Sum256([]byte(token))

		valid, err := s.ValidSession(r.Context(), hash[:])
		if err != nil {
			l.Error("check session", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if !valid {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
