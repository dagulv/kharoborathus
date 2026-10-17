package safehttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientRefusesPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	if _, err := NewClient(time.Second).Get(srv.URL); !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("err %v", err)
	}
}
