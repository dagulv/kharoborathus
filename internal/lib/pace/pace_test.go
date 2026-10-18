package pace

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPacerSpacesHost(t *testing.T) {
	gap := 50 * time.Millisecond
	p := New(gap)
	ctx := context.Background()

	start := time.Now()

	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if err := p.Wait(ctx, "https://www.a.example/x"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	if elapsed := time.Since(start); elapsed < 2*gap {
		t.Errorf("three requests to one host took %v, want at least %v", elapsed, 2*gap)
	}

	start = time.Now()

	if err := p.Wait(ctx, "https://b.example/"); err != nil {
		t.Fatal(err)
	}

	if elapsed := time.Since(start); elapsed >= gap {
		t.Errorf("another host waited %v", elapsed)
	}
}

func TestPacerCancel(t *testing.T) {
	p := New(time.Hour)
	p.Wait(context.Background(), "https://a.example/")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := p.Wait(ctx, "https://a.example/"); err == nil {
		t.Error("cancelled wait returned nil")
	}
}

func TestNilPacer(t *testing.T) {
	var p *Pacer

	if err := p.Wait(context.Background(), "https://a.example/"); err != nil {
		t.Error(err)
	}
}
