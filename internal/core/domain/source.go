package domain

import (
	"database/sql"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/query"
)

const (
	StatusProposed = "proposed"
	StatusActive   = "active"
	StatusFlagged  = "flagged"
)

type Source struct {
	ID        string            `json:"id"`
	DomainID  string            `json:"domainId"`
	Adapter   string            `json:"adapter"` // type of source (rss, etc)
	URL       string            `json:"url"`
	Enabled   bool              `json:"enabled"`
	Rate      int               `json:"rate"`
	Lang      string            `json:"lang"`
	Metadata  map[string]string `json:"metadata"` // maybe needed for rare occurrences (like login, disclaimers, etc)
	Status    string            `json:"status"`   // proposed | active | flagged
	CreatedAt time.Time         `json:"createdAt"`
	DeletedAt *time.Time        `json:"deletedAt"`
}

type SourceFilter struct {
	query.Filter

	Enabled  sql.Null[bool]   `json:"enabled"`
	Status   sql.Null[string] `json:"status"`
	DomainID sql.Null[string] `json:"domainId"`
	Adapter  sql.Null[string] `json:"adapter"`
}

// What a url can be added as, best first, for the admin.
type Listing struct {
	Candidates []Candidate `json:"candidates"`
	Archive    *Archive    `json:"archive,omitempty"`
}

type Candidate struct {
	Adapter string    `json:"adapter"`
	URL     string    `json:"url"`
	Title   string    `json:"title,omitzero"`
	Items   int       `json:"items,omitzero"`
	Latest  time.Time `json:"latest,omitzero"`
}

// A site's whole history, read once on backfill.
type Archive struct {
	URL   string `json:"url"`
	Total int    `json:"total"`
}
