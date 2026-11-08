package discovery

import "time"

type SearchItem struct {
	ID         string
	Entity     string
	Action     string
	Query      string
	Lang       string
	BaseURL    string
	FromPostID string
	SearchedAt *time.Time
}

// SearchResult hides its URL from the filter prompt, but the stored result needs it.
type storedResult struct {
	URL string `json:"url"`
	SearchResult
}
