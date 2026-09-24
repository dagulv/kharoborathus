package adapter

import (
	"context"
	"errors"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

var (
	ErrUnavailable = errors.New("adapter: unavailable")
	ErrIncomplete  = errors.New("adapter: incomplete answer")
)

type LLM interface {
	Schedule() schedule.Schedule
	Prompt(ctx context.Context, req Request) (string, error)
	Unmarshal(ctx context.Context, req Request, out any) error
}

type Classifier interface {
}

type Request struct {
	System      string
	Prompt      string
	Model       string
	Temperature *float64
}
