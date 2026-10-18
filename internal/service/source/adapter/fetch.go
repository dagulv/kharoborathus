package adapter

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
)

const maxPageSize = 5 << 20

func FetchHTML(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	page, _, err := FetchPage(ctx, client, url)

	return page, err
}

func FetchPage(ctx context.Context, client *http.Client, url string) ([]byte, string, error) {
	b, final, mt, err := fetch(ctx, client, url, "text/html,application/xhtml+xml")
	if err != nil {
		return nil, "", fmt.Errorf("err fetch html: %w", err)
	}

	if !IsHTML(mt) {
		return nil, "", fmt.Errorf("err fetch html: content type %s", mt)
	}

	return b, final, nil
}

// FetchAny fetches a page or a feed and returns the body, the final url and the media type.
func FetchAny(ctx context.Context, client *http.Client, url string) ([]byte, string, string, error) {
	b, final, mt, err := fetch(ctx, client, url, "text/html,application/xhtml+xml,application/rss+xml,application/atom+xml,application/feed+json,application/xml;q=0.9,*/*;q=0.8")
	if err != nil {
		return nil, "", "", fmt.Errorf("err fetch: %w", err)
	}

	return b, final, mt, nil
}

// A missing or unparsable content type counts as html, as it always has.
func IsHTML(mt string) bool {
	return mt == "" || mt == "text/html" || mt == "application/xhtml+xml"
}

func fetch(ctx context.Context, client *http.Client, url, accept string) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("err new request: %w", err)
	}

	req.Header.Set("User-Agent", "kharoborathus/1.0")
	req.Header.Set("Accept", accept)

	res, err := client.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, "", "", fmt.Errorf("status %s", res.Status)
	}

	mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))

	b, err := io.ReadAll(io.LimitReader(res.Body, maxPageSize))
	if err != nil {
		return nil, "", "", fmt.Errorf("err read body: %w", err)
	}

	return b, res.Request.URL.String(), mt, nil
}
