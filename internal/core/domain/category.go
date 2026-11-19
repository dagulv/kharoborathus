package domain

import "time"

type Category struct {
	Key       string            `json:"key"`
	Enabled   bool              `json:"enabled"`
	Terms     map[string]string `json:"terms"` // lang to term
	Tags      []string          `json:"tags"`
	CreatedAt time.Time         `json:"createdAt"`
}
