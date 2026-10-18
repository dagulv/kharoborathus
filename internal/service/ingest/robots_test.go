package ingest

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRobots(t *testing.T) {
	cases := []struct {
		name, body, path string
		want             bool
	}{
		{"wildcard disallow", "User-agent: *\nDisallow: /private", "/private/a", false},
		{"wildcard other path", "User-agent: *\nDisallow: /private", "/news/a", true},
		{"specific group wins", "User-agent: *\nDisallow: /\n\nUser-agent: Kharoborathus\nDisallow: /x", "/a", true},
		{"specific group rule", "User-agent: *\nDisallow: /\n\nUser-agent: kharoborathus\nDisallow: /x", "/x/y", false},
		{"specific empty disallow", "User-agent: *\nDisallow: /\n\nUser-agent: kharoborathus\nDisallow:", "/a", true},
		{"longest match allow", "User-agent: *\nDisallow: /a\nAllow: /a/b", "/a/b/c", true},
		{"longest match disallow", "User-agent: *\nDisallow: /a\nAllow: /a/b", "/a/c", false},
		{"tie allows", "User-agent: *\nDisallow: /a\nAllow: /a", "/a", true},
		{"star and end", "User-agent: *\nDisallow: /*.pdf$", "/f.pdf", false},
		{"end anchors", "User-agent: *\nDisallow: /*.pdf$", "/f.pdf?x=1", true},
		{"comments and case", "USER-AGENT: * # all\nDISALLOW: /x # no", "/x", false},
		{"grouped agents", "User-agent: other\nUser-agent: *\nDisallow: /x", "/x", false},
		{"other agent only", "User-agent: other\nDisallow: /", "/x", true},
		{"empty", "", "/x", true},
	}

	for _, c := range cases {
		if got := parseRobots([]byte(c.body), robotsAgent).allowed(c.path); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func robotsServer(t *testing.T, status int, body string) (*httptest.Server, *int) {
	t.Helper()

	var requests int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/robots.txt" {
			t.Errorf("request to %s", r.URL.Path)
		}

		if r.UserAgent() != userAgent {
			t.Errorf("user agent %q", r.UserAgent())
		}

		requests++
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))

	t.Cleanup(srv.Close)

	return srv, &requests
}

func TestRobotsAllowed(t *testing.T) {
	srv, requests := robotsServer(t, http.StatusOK, "User-agent: *\nDisallow: /private")
	r := NewRobots(srv.Client(), newPacer(0))

	if ok, err := r.Allowed(context.Background(), slog.New(slog.DiscardHandler), srv.URL+"/private/1"); err != nil || ok {
		t.Errorf("private: %v %v", ok, err)
	}

	if ok, err := r.Allowed(context.Background(), slog.New(slog.DiscardHandler), srv.URL+"/news/1"); err != nil || !ok {
		t.Errorf("news: %v %v", ok, err)
	}

	if *requests != 1 {
		t.Errorf("%d robots requests", *requests)
	}
}

func TestRobotsMissing(t *testing.T) {
	srv, _ := robotsServer(t, http.StatusNotFound, "")

	if ok, err := NewRobots(srv.Client(), newPacer(0)).Allowed(context.Background(), slog.New(slog.DiscardHandler), srv.URL+"/x"); err != nil || !ok {
		t.Errorf("got %v %v", ok, err)
	}
}

func TestRobotsServerError(t *testing.T) {
	srv, requests := robotsServer(t, http.StatusServiceUnavailable, "")
	r := NewRobots(srv.Client(), newPacer(0))

	for range 2 {
		if _, err := r.Allowed(context.Background(), slog.New(slog.DiscardHandler), srv.URL+"/x"); err == nil {
			t.Error("no error")
		}
	}

	if *requests != 2 {
		t.Errorf("%d robots requests, errors must not be cached", *requests)
	}
}
