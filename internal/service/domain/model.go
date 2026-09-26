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
	BaseURL   string    `json:"baseUrl"` // scheme and host without www, ex https://dn.se
	Metadata  Metadata  `json:"metadata"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Metadata struct {
	Trim map[string]json.RawMessage `json:"trim,omitempty"` // trim ruleset by data format, ex html
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
