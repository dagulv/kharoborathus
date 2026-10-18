package adapter

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
)

const maxPageSize = 5 << 20

const anyAccept = "text/html,application/xhtml+xml,application/rss+xml,application/atom+xml,application/feed+json,application/xml;q=0.9,*/*;q=0.8"

type response struct {
	body      []byte
	url       string // after redirects
	mediaType string
	header    http.Header
}

func FetchHTML(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	page, _, err := FetchPage(ctx, client, url)

	return page, err
}

func FetchPage(ctx context.Context, client *http.Client, url string) ([]byte, string, error) {
	res, err := fetch(ctx, client, url, "text/html,application/xhtml+xml")
	if err != nil {
		return nil, "", fmt.Errorf("err fetch html: %w", err)
	}

	if !IsHTML(res.mediaType) {
		return nil, "", fmt.Errorf("err fetch html: content type %s", res.mediaType)
	}

	return res.body, res.url, nil
}

// FetchAny fetches a page or a feed and returns the body, the final url and the media type.
func FetchAny(ctx context.Context, client *http.Client, url string) ([]byte, string, string, error) {
	res, err := fetch(ctx, client, url, anyAccept)
	if err != nil {
		return nil, "", "", fmt.Errorf("err fetch: %w", err)
	}

	return res.body, res.url, res.mediaType, nil
}

// A missing or unparsable content type counts as html, as it always has.
func IsHTML(mt string) bool {
	return mt == "" || mt == "text/html" || mt == "application/xhtml+xml"
}

func fetch(ctx context.Context, client *http.Client, url, accept string) (response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return response{}, fmt.Errorf("err new request: %w", err)
	}

	req.Header.Set("User-Agent", "kharoborathus/1.0")
	req.Header.Set("Accept", accept)

	res, err := client.Do(req)
	if err != nil {
		return response{}, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return response{}, fmt.Errorf("status %s", res.Status)
	}

	mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))

	b, err := io.ReadAll(io.LimitReader(res.Body, maxPageSize))
	if err != nil {
		return response{}, fmt.Errorf("err read body: %w", err)
	}

	return response{body: b, url: res.Request.URL.String(), mediaType: mt, header: res.Header}, nil
}
