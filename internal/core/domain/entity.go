package domain

import (
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/query"
)

var EntityKinds = []string{"company", "organization", "technology", "product", "strategy", "concept"}

type Entity struct {
	Key       string              `json:"key"`
	Name      string              `json:"name"`
	Kind      string              `json:"kind"`
	Status    string              `json:"status"` // proposed | active
	Enabled   bool                `json:"enabled"`
	Focus     bool                `json:"focus"`
	Terms     map[string][]string `json:"terms"` // lang to terms
	Tags      []string            `json:"tags"`
	Strength  int                 `json:"strength"` // Posts linked in the last 30 days, from any source
	CreatedAt time.Time           `json:"createdAt"`
}

// An entity a post was linked to.
type LinkedEntity struct {
	Key     string   `json:"key"`
	Status  string   `json:"status"`
	Focus   bool     `json:"focus"`
	Names   []string `json:"names"`   // Lowercased name and terms
	Aliases []string `json:"aliases"` // Alias keys
	Term    string   `json:"term"`    // The extracted term that grounded it in this post
}

type EntityFilter struct {
	query.Filter

	Kind     string `query:"kind"`
	Tag      string `query:"tag"`
	Category string `query:"category"`
}
