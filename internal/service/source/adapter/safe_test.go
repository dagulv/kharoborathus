package adapter

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/safehttp"
)

func TestAdaptersRefusePrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("private address fetched")
	}))
	defer srv.Close()

	l := slog.New(slog.DiscardHandler)

	if _, err := NewRSS(nil, l).FetchFromSource(context.Background(), srv.URL); !errors.Is(err, safehttp.ErrPrivateAddress) {
		t.Errorf("rss: %v", err)
	}

	if _, err := NewPage(nil, nil, l).FetchFromSource(context.Background(), srv.URL); !errors.Is(err, safehttp.ErrPrivateAddress) {
		t.Errorf("page: %v", err)
	}
}
