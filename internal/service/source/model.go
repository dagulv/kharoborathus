package source

import (
	"database/sql"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/query"
)

const (
	StatusProposed = "proposed"
	StatusActive   = "active"
	StatusDormant  = "dormant"
)

type Source struct {
	ID         string            `json:"id"`
	DomainID   string            `json:"domainId"`
	Adapter    string            `json:"adapter"` // type of source (rss, etc)
	URL        string            `json:"url"`
	Enabled    bool              `json:"enabled"`
	Status     string            `json:"status"` // proposed | active | dormant
	Origin     string            `json:"origin"` // manual | discovered
	Rate       int               `json:"rate"`
	CreatedAt  time.Time         `json:"createdAt"`
	TargetTags []string          `json:"targetTags"`
	Metadata   map[string]string `json:"metadata"` // maybe needed for rare occurrences (like login, disclaimers, etc)
	Lang       string            `json:"-"`        // set on backfills, for posts that carry no language
}

type Filter struct {
	query.Filter

	Enabled sql.Null[bool]   `json:"enabled"`
	Status  sql.Null[string] `json:"status"`
}
