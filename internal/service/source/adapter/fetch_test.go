package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchPageFollowsRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/old", http.RedirectHandler("/new", http.StatusMovedPermanently))
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<p>Ny</p>"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	page, final, err := FetchPage(context.Background(), srv.Client(), srv.URL+"/old")
	if err != nil {
		t.Fatal(err)
	}

	if final != srv.URL+"/new" || string(page) != "<p>Ny</p>" {
		t.Errorf("final %s page %s", final, page)
	}
}
