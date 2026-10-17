package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
)

func TestProcessPrivateAddress(t *testing.T) {
	p := newPacer(0)
	in := &Ingester{pacer: p, robots: NewRobots(safehttp.NewClient(time.Second), p)}

	res, err := in.process(context.Background(), Link{URL: "http://127.0.0.1:1/news"}, nil)
	if err != nil || res.status != statusFailed || res.reason != "private address" {
		t.Errorf("res %+v err %v", res, err)
	}
}

func TestProcessOffHostRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<p>Annan sajt</p>"))
	}))
	defer target.Close()

	moved := strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + "/x"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}

		http.Redirect(w, r, moved, http.StatusFound)
	}))
	defer srv.Close()

	p := newPacer(0)
	in := &Ingester{client: srv.Client(), pacer: p, robots: NewRobots(srv.Client(), p)}

	res, err := in.process(context.Background(), Link{URL: srv.URL + "/news"}, nil)
	if err != nil || res.status != statusFailed || res.reason != "redirected to localhost" {
		t.Errorf("res %+v err %v", res, err)
	}
}
