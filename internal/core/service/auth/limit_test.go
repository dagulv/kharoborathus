package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimitVerify(t *testing.T) {
	var inFlight, peak atomic.Int32
	release := make(chan struct{})

	verify := limitVerify(4, func(pw, stored string) (bool, error) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}

		<-release
		inFlight.Add(-1)

		return true, nil
	})

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if ok, err := verify(context.Background(), "pw", "h"); !ok || err != nil {
				t.Errorf("verify: %v, %v", ok, err)
			}
		})
	}

	time.Sleep(100 * time.Millisecond)

	if got := inFlight.Load(); got != 4 {
		t.Errorf("in flight with slots full: %d, want 4", got)
	}

	// A request that gives up while waiting must not take a slot.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := verify(ctx, "pw", "h"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled wait: %v", err)
	}

	close(release)
	wg.Wait()

	if peak.Load() != 4 {
		t.Errorf("peak %d, want 4", peak.Load())
	}
}
