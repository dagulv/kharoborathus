package domain

import (
	"encoding/json"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/query"
)

// Shared by raw_posts and discovery_posts.
const (
	StatusPending   = "pending"
	StatusAccepted  = "accepted"
	StatusRejected  = "rejected"
	StatusDuplicate = "duplicate"
	StatusFailed    = "failed"
)

type RawPost struct {
	ID         string     `json:"id"`
	SourceID   string     `json:"sourceId"`
	URL        string     `json:"url"`
	Lang       string     `json:"lang"`
	DomainID   string     `json:"domainId"`
	RawContent string     `json:"rawContent"` // Different types depending on type of source, e.g. HTML etc.
	Content    string     `json:"content"`
	Simhash    *int64     `json:"-"`      // NULL when the content has no words
	Status     string     `json:"status"` // pending | accepted | rejected | duplicate | failed
	CreatedAt  time.Time  `json:"createdAt"`
	Title      string     `json:"title"` // Not stored, carried from the fetch or trim to the post
	Authors    []string   `json:"authors"`
	PostedAt   *time.Time `json:"postedAt"`
}

type Post struct {
	ID           string     `json:"id"`
	SourceID     string     `json:"sourceId"`
	URL          string     `json:"url"`
	Lang         string     `json:"lang"`
	DomainID     string     `json:"domainId"` // Used for icon/brand and link, resolved from url
	Title        string     `json:"title"`
	ShortContent string     `json:"shortContent"` // Plain text short content, llm summary of content
	Content      string     `json:"content"`      // Markdown content
	Tags         []string   `json:"tags"`
	PostedAt     *time.Time `json:"postedAt"`
	Authors      []string   `json:"authors"`
	CreatedAt    time.Time  `json:"createdAt"`
	Simhash      *int64     `json:"-"`
}

type PostFilter struct {
	query.Filter

	Tags []string `query:"tag"`
}

type SourceDiscoveredByPost struct {
	SourceID string `json:"sourceId"`
	PostID   string `json:"postId"`
}

type DiscoverySearchQuery struct {
	ID               string          `json:"id"`
	NormalizedEntity string          `json:"normalizedEntity"`
	Action           string          `json:"action"`
	Query            string          `json:"query"`
	SearchedAt       *time.Time      `json:"searchedAt"`
	Result           json.RawMessage `json:"result"`
	CreatedAt        time.Time       `json:"createdAt"`
}

type DiscoverySearchQueryPost struct {
	QueryID string `json:"queryId"`
	PostID  string `json:"postId"`
}

type DiscoveryPost struct {
	ID                     string     `json:"id"`
	URL                    string     `json:"url"`
	FromPostID             string     `json:"fromPostId"`
	DiscoverySearchQueryID *string    `json:"discoverySearchQueryId"` // NULL for links found in a post, or once the query is deleted
	Status                 string     `json:"status"`                 // pending | accepted | rejected | duplicate | failed
	CreatedAt              time.Time  `json:"createdAt"`
	CheckedAt              *time.Time `json:"checkedAt"`
}

type DiscoveryRelatedPost struct {
	FromPostID string         `json:"fromPostId"`
	ToPostID   string         `json:"toPostId"`
	Relation   string         `json:"relation"` // discovered | duplicate_of | same_event | follows_up | translation_of
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"createdAt"`
}
