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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("err new html request: %w", err)
	}

	req.Header.Set("User-Agent", "kharoborathus/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("err fetch html: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("err fetch html: status %s", res.Status)
	}

	if mt, _, err := mime.ParseMediaType(res.Header.Get("Content-Type")); err == nil && mt != "text/html" && mt != "application/xhtml+xml" {
		return nil, fmt.Errorf("err fetch html: content type %s", mt)
	}

	b, err := io.ReadAll(io.LimitReader(res.Body, maxPageSize))
	if err != nil {
		return nil, fmt.Errorf("err read html: %w", err)
	}

	return b, nil
}
