package fetcher

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
)

func TestAdaptersRefusePrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("private address fetched")
	}))
	defer srv.Close()

	l := slog.New(slog.DiscardHandler)

	if _, err := NewRSS(nil, nil, l).Fetch(context.Background(), slog.New(slog.DiscardHandler), srv.URL); !errors.Is(err, safehttp.ErrPrivateAddress) {
		t.Errorf("rss: %v", err)
	}

	// A refused detection counts as no archive. The handler fails the test if it is reached.
	if _, err := NewRSS(nil, nil, l).Backfill(context.Background(), slog.New(slog.DiscardHandler), srv.URL); !errors.Is(err, port.ErrNoArchive) {
		t.Errorf("rss backfill: %v", err)
	}

	if _, err := NewPage(nil, nil, nil, nil, l).Fetch(context.Background(), slog.New(slog.DiscardHandler), srv.URL); !errors.Is(err, safehttp.ErrPrivateAddress) {
		t.Errorf("page: %v", err)
	}
}
