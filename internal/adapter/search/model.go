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
	// Saved but not yet filtered, after a failed filter.
	Results []SearchResult
	// Filtered, or rejected by the search API.
	Done bool
}
