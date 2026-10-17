package ingest

import (
	"context"
	"time"
)

type pacer struct {
	gap  time.Duration
	last map[string]time.Time
}

func newPacer(gap time.Duration) *pacer {
	return &pacer{gap: gap, last: make(map[string]time.Time)}
}

func (p *pacer) Wait(ctx context.Context, host string) error {
	if wait := time.Until(p.last[host].Add(p.gap)); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	p.last[host] = time.Now()

	return nil
}
