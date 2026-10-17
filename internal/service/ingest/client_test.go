package ingest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSafeClientRefusesPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	if _, err := safeClient(time.Second).Get(srv.URL); !errors.Is(err, errPrivateAddress) {
		t.Errorf("err %v", err)
	}
}

func TestProcessPrivateAddress(t *testing.T) {
	p := newPacer(0)
	in := &Ingester{pacer: p, robots: NewRobots(safeClient(time.Second), p)}

	res, err := in.process(context.Background(), Link{URL: "http://127.0.0.1:1/news"}, nil)
	if err != nil || res.status != statusFailed || res.reason != "private address" {
		t.Errorf("res %+v err %v", res, err)
	}
}
