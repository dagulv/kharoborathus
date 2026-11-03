package domain

import (
	"encoding/json"
	"time"
)

type Log struct {
	ID         int64           `json:"id"`
	Type       string          `json:"type"` // deepseek | jev | brave | rss | page | trim | ingest | robots | homepage | wordpress
	Level      string          `json:"level"`
	Request    json.RawMessage `json:"request"`
	Response   json.RawMessage `json:"response"` // nil when the call got no response
	Status     *int            `json:"status"`
	DurationMs int64           `json:"durationMs"`
	Error      *string         `json:"error"`
	Metadata   map[string]any  `json:"metadata"`
	CreatedAt  time.Time       `json:"createdAt"`
}
