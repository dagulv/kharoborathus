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

	Enabled sql.Null[bool]   `json:"enabled"`
	Status  sql.Null[string] `json:"status"`
}
