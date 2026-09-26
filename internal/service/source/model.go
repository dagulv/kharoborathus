package source

import (
	"database/sql"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/query"
)

type Source struct {
	ID         string            `json:"id"`
	DomainID   string            `json:"domainId"`
	Adapter    string            `json:"adapter"` // type of source (rss, etc)
	URL        string            `json:"url"`
	Active     bool              `json:"active"`
	Rate       int               `json:"rate"`
	CreatedAt  time.Time         `json:"createdAt"`
	TargetTags []string          `json:"targetTags"`
	Metadata   map[string]string `json:"metadata"` // maybe needed for rare occurrences (like login, disclaimers, etc)
}

type Filter struct {
	query.Filter

	Active sql.Null[bool] `json:"active"`
}
