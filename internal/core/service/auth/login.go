package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/password"
	"github.com/jackc/pgx/v5"
)

type Users interface {
	UserByEmail(ctx context.Context, email string) (domain.User, error)
}

type LoginSessions interface {
	CreateSession(ctx context.Context, tokenHash []byte, userID string, days int) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
}

// Login goes on public. Logout and the session check go on protected, behind RequireSession.
func Routes(public, protected *http.ServeMux, users Users, sessions LoginSessions, l *slog.Logger) {
	// Verified against when the email is unknown, so a miss takes as long as a wrong password.
	dummyHash, err := password.Hash(rand.Text())
	if err != nil {
		panic(err)
	}

	public.HandleFunc("POST /admin/login", login(users, sessions, dummyHash, l))
	protected.HandleFunc("POST /admin/logout", logout(sessions, l))
	protected.HandleFunc("GET /admin/session", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func login(users Users, sessions LoginSessions, dummyHash string, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}

		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		user, err := users.UserByEmail(r.Context(), strings.TrimSpace(body.Email))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			l.Error("get user", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		stored := user.PasswordHash
		if user.ID == "" {
			stored = dummyHash
		}

		valid, err := password.Verify(body.Password, stored)
		if err != nil {
			l.Error("verify password", "user", user.ID, "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if !valid || user.ID == "" {
			http.Error(w, "wrong email or password", http.StatusUnauthorized)
			return
		}

		b := make([]byte, 32)
		rand.Read(b)

		token := base64.RawURLEncoding.EncodeToString(b)
		hash := sha256.Sum256([]byte(token))

		if err := sessions.CreateSession(r.Context(), hash[:], user.ID, SessionDays); err != nil {
			l.Error("create session", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, sessionCookie(token, SessionDays*24*60*60))
		w.WriteHeader(http.StatusNoContent)
	}
}

func logout(sessions LoginSessions, l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// RequireSession already checked the cookie.
		c, _ := r.Cookie(SessionCookie)
		hash := sha256.Sum256([]byte(c.Value))

		if err := sessions.DeleteSession(r.Context(), hash[:]); err != nil {
			l.Error("delete session", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, sessionCookie("", -1))
		w.WriteHeader(http.StatusNoContent)
	}
}

// Host-only on the API host. The admin SPA is on the same site, so Strict still sends it.
func sessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/admin",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}
