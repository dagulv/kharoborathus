package domain

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type Domain struct {
	ID        string    `json:"id"`
	BaseURL   string    `json:"baseUrl"`
	Metadata  Metadata  `json:"metadata"`
	CreatedAt time.Time `json:"createdAt"`
}

type Metadata struct {
	Trim map[string]json.RawMessage `json:"trim,omitempty"`
}

func BaseURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	if host == "" {
		return "", fmt.Errorf("domain: no host in %q", rawURL)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = "https"
	}

	return scheme + "://" + host, nil
}
