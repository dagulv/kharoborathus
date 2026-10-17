package discovery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

func testBrave(t *testing.T, handler http.HandlerFunc) brave {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return brave{client: srv.Client(), url: srv.URL, key: "secret"}
}

func TestBraveSearch(t *testing.T) {
	b := testBrave(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("X-Subscription-Token") != "secret" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("request %s %v", r.Method, r.Header)
		}

		q := r.URL.Query()
		want := map[string]string{
			"q": "Volvo köper Northvolt", "country": "SE", "search_lang": "sv", "freshness": "pw",
			"count": "20", "result_filter": "web,news", "extra_snippets": "true",
		}

		for k, v := range want {
			if q.Get(k) != v {
				t.Errorf("%s = %q", k, q.Get(k))
			}
		}

		w.Write([]byte(`{
			"web": {"results": [
				{"url": "https://a.example/1", "title": "A", "description": "Da", "extra_snippets": ["Ea"], "page_age": "2026-10-16T08:00:00", "meta_url": {"hostname": "a.example"}},
				{"url": "https://b.example/2", "title": "B", "meta_url": {"hostname": "b.example"}}
			]},
			"news": {"results": [
				{"url": "https://b.example/2", "title": "B again"},
				{"url": "https://c.example/3", "title": "C", "meta_url": {"hostname": "c.example"}}
			]}
		}`))
	})

	got, err := b.Search(context.Background(), Query{Q: "Volvo köper Northvolt", Lang: "sv"})
	if err != nil {
		t.Fatal(err)
	}

	var urls []string

	for _, r := range got {
		urls = append(urls, r.URL)
	}

	if !slices.Equal(urls, []string{"https://a.example/1", "https://b.example/2", "https://c.example/3"}) {
		t.Fatalf("urls %v", urls)
	}

	a := got[0]
	if a.Title != "A" || a.Description != "Da" || !slices.Equal(a.ExtraSnippets, []string{"Ea"}) || a.PageAge != "2026-10-16T08:00:00" || a.Hostname != "a.example" {
		t.Errorf("result %+v", a)
	}

	if got[1].Title != "B" {
		t.Errorf("web result not kept over news duplicate: %+v", got[1])
	}
}

func TestBraveOtherLanguage(t *testing.T) {
	b := testBrave(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("search_lang") {
			t.Errorf("search_lang %q", r.URL.Query().Get("search_lang"))
		}

		w.Write([]byte(`{}`))
	})

	if got, err := b.Search(context.Background(), Query{Q: "x", Lang: "de"}); err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestBraveErrors(t *testing.T) {
	cases := map[int]error{
		http.StatusTooManyRequests:     adapter.ErrUnavailable,
		http.StatusServiceUnavailable:  adapter.ErrUnavailable,
		http.StatusUnprocessableEntity: errSearchRejected,
		http.StatusBadRequest:          errSearchRejected,
		http.StatusUnauthorized:        nil,
	}

	for status, want := range cases {
		b := testBrave(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		})

		_, err := b.Search(context.Background(), Query{Q: "x"})

		switch {
		case err == nil:
			t.Errorf("%d: no error", status)
		case want != nil && !errors.Is(err, want):
			t.Errorf("%d: got %v", status, err)
		case want == nil && (errors.Is(err, adapter.ErrUnavailable) || errors.Is(err, errSearchRejected)):
			t.Errorf("%d: got %v", status, err)
		}
	}
}

func TestBraveUnreadable(t *testing.T) {
	b := testBrave(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"web": {"results": [{"url": 1}]}}`))
	})

	if _, err := b.Search(context.Background(), Query{Q: "x"}); !errors.Is(err, errSearchUnreadable) {
		t.Errorf("got %v", err)
	}
}

func TestBraveInvalidToken(t *testing.T) {
	b := testBrave(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"type":"ErrorResponse","error":{"status":422,"code":"SUBSCRIPTION_TOKEN_INVALID","detail":"The provided subscription token is invalid."}}`))
	})

	_, err := b.Search(context.Background(), Query{Q: "x"})
	if err == nil || errors.Is(err, errSearchRejected) {
		t.Errorf("got %v", err)
	}
}
