package adapter

import (
	"context"
	"errors"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

var (
	ErrUnavailable = errors.New("adapter: unavailable")
	// Always wrapped with ErrUnavailable. Peak hours are expected, an outage is not.
	ErrPeakHours  = errors.New("adapter: peak hours")
	ErrIncomplete = errors.New("adapter: incomplete answer")
)

type LLM interface {
	Schedule() schedule.Schedule
	Prompt(ctx context.Context, req Request) (string, error)
	Unmarshal(ctx context.Context, req Request, out any) error
}

type Classifier interface {
	Noul(ctx context.Context, state any, questions map[string]NoulQuestion) (map[string]float64, error)
}

type NoulQuestion struct {
	Instructions string        `json:"instructions"`
	Criteria     *NoulCriteria `json:"criteria,omitempty"`
}

type NoulCriteria struct {
	True  string `json:"true"`
	False string `json:"false"`
}

type Request struct {
	System      string
	Prompt      string
	Model       string
	Temperature *float64
}
