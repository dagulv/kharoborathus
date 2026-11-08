package domain

import "time"

const (
	TagTypeQuality   = "quality"
	TagTypeSentiment = "sentiment"
)

type Tag struct {
	Key       string    `json:"key"`
	Enabled   bool      `json:"enabled"`
	Status    string    `json:"status"` // proposed | active
	Type      string    `json:"type"`   // sentiment | quality etc
	CreatedAt time.Time `json:"createdAt"`
	Terms     []string  `json:"terms"` // In one language, loaded for Jev
}

type TagTerm struct {
	Key  string `json:"key"`
	Lang string `json:"lang"`
	Term string `json:"term"`
}
