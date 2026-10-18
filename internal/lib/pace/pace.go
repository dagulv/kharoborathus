package pace

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Pacer spaces requests to one host at least gap apart, across goroutines.
// It waits before the request instead of inside a transport, since a client timeout would count the wait.
type Pacer struct {
	gap  time.Duration
	mu   sync.Mutex
	next map[string]time.Time
}

func New(gap time.Duration) *Pacer {
	return &Pacer{gap: gap, next: make(map[string]time.Time)}
}

// Wait blocks until the host of rawURL is free. Slots go out in call order, so concurrent callers queue.
// A nil Pacer never waits.
func (p *Pacer) Wait(ctx context.Context, rawURL string) error {
	if p == nil {
		return nil
	}

	// The fetch fails on it anyway.
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}

	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")

	p.mu.Lock()
	slot := time.Now()
	if next := p.next[host]; next.After(slot) {
		slot = next
	}
	p.next[host] = slot.Add(p.gap)
	p.mu.Unlock()

	wait := time.Until(slot)
	if wait <= 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
