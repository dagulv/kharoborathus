package domain

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrSourceExists = errors.New("source exists")

type Stats struct {
	Sources        int            `json:"sources"`
	EnabledSources int            `json:"enabledSources"`
	FlaggedSources int            `json:"flaggedSources"`
	Posts          int            `json:"posts"`
	PostsToday     int            `json:"postsToday"`
	SearchesToday  int            `json:"searchesToday"`
	Links          map[string]int `json:"links"` // discovery_posts by status
}

type AdminSource struct {
	Source

	DomainURL    string     `json:"domainUrl"`
	PostCount    int        `json:"postCount"`
	LastPostedAt *time.Time `json:"lastPostedAt"`
}

type NewSource struct {
	URL        string `json:"url"`
	Adapter    string `json:"adapter"`
	ListingURL string `json:"listingUrl"`
	Rate       int    `json:"rate"`
	Lang       string `json:"lang"`
}

type AdminPostFilter struct {
	SourceID string
	Tag      string
	Search   string
	Page     int
}

type PostPage struct {
	Posts    []Post `json:"posts"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
}

type AdminPost struct {
	Post

	SourceURL string `json:"sourceUrl"`
}

var ErrMergeSelf = errors.New("merge tag into itself")

type TagSummary struct {
	Tag

	PostCount int `json:"postCount"`
}

type TagProposal struct {
	Key            string    `json:"key"`
	Terms          []string  `json:"terms"`
	PostCount      int       `json:"postCount"`
	DomainCount    int       `json:"domainCount"`
	LastProposedAt time.Time `json:"lastProposedAt"`
}

type TagAlias struct {
	Alias string `json:"alias"`
	Key   string `json:"key"`
}

type TagOverview struct {
	Tags      []TagSummary  `json:"tags"`
	Proposals []TagProposal `json:"proposals"`
	Aliases   []TagAlias    `json:"aliases"`
}

type LogSummary struct {
	ID         int64          `json:"id"`
	Type       string         `json:"type"`
	Level      string         `json:"level"`
	Method     *string        `json:"method"`
	URL        *string        `json:"url"`
	Status     *int           `json:"status"`
	DurationMs int64          `json:"durationMs"`
	Error      *string        `json:"error"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"createdAt"`
}

type LogDetail struct {
	LogSummary

	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

type LogFilter struct {
	Type      string
	Level     string
	MetaKey   string
	MetaValue string
	Page      int
}

type LogPage struct {
	Logs    []LogSummary `json:"logs"`
	HasNext bool         `json:"hasNext"`
	Page    int          `json:"page"`
}
