package auth

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"mime"
	"net/http"
)

type Sessions interface {
	ValidSession(ctx context.Context, tokenHash []byte) (bool, error)
}

const (
	SessionCookie = "session"
	SessionDays   = 30
)

// Sessions store only the sha256 of the token string, as the web app's login did.
func RequireSession(s Sessions, l *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil || c.Value == "" {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		hash := sha256.Sum256([]byte(c.Value))

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

// A cross-origin JSON write needs a CORS preflight, so this keeps plain form posts from other sites out.
func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPatch {
			next.ServeHTTP(w, r)
			return
		}

		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}

		next.ServeHTTP(w, r)
	})
}
