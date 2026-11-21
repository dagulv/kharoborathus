package auth

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/lib/password"
)

func TestLoginFlow(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	l := slog.New(slog.DiscardHandler)

	users := postgres.NewUserStore(db)
	sessions := postgres.NewSessionStore(db)

	hash, err := password.Hash("pw")
	if err != nil {
		t.Fatal(err)
	}

	if err := users.CreateUser(ctx, "a@example.com", hash); err != nil {
		t.Fatal(err)
	}

	protected := http.NewServeMux()
	admin := http.NewServeMux()
	Routes(admin, protected, &users, &sessions, l)
	admin.Handle("/admin/", RequireSession(&sessions, l, protected))
	h := RequireJSON(admin)

	do := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if method != http.MethodGet {
			req.Header.Set("Content-Type", "application/json")
		}

		if cookie != nil {
			req.AddCookie(cookie)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		return rec
	}

	for name, body := range map[string]string{
		"wrong password": `{"email":"a@example.com","password":"px"}`,
		"unknown email":  `{"email":"b@example.com","password":"pw"}`,
	} {
		rec := do(http.MethodPost, "/admin/login", body, nil)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("%s: %d, %q", name, rec.Code, rec.Header().Get("Set-Cookie"))
		}
	}

	if rec := do(http.MethodPost, "/admin/login", `{`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad body: %d", rec.Code)
	}

	rec := do(http.MethodPost, "/admin/login", `{"email":" a@example.com ","password":"pw"}`, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies: %v", cookies)
	}

	c := cookies[0]
	if c.Name != SessionCookie || c.Value == "" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/admin" || c.MaxAge != 2592000 || c.Domain != "" {
		t.Errorf("cookie: %+v", c)
	}

	session := &http.Cookie{Name: SessionCookie, Value: c.Value}

	if rec := do(http.MethodGet, "/admin/session", "", session); rec.Code != http.StatusNoContent {
		t.Errorf("session: %d", rec.Code)
	}

	if rec := do(http.MethodGet, "/admin/session", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("session without cookie: %d", rec.Code)
	}

	rec = do(http.MethodPost, "/admin/logout", "{}", session)
	if rec.Code != http.StatusNoContent {
		t.Errorf("logout: %d", rec.Code)
	}

	if cleared := rec.Result().Cookies(); len(cleared) != 1 || cleared[0].MaxAge >= 0 || cleared[0].Path != "/admin" {
		t.Errorf("logout cookie: %v", cleared)
	}

	if rec := do(http.MethodGet, "/admin/session", "", session); rec.Code != http.StatusUnauthorized {
		t.Errorf("session after logout: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(`{"email":"a@example.com","password":"pw"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain login: %d", rec.Code)
	}
}
