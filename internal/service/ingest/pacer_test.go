package ingest

import (
	"context"
	"testing"
	"time"
)

func TestPacer(t *testing.T) {
	gap := 100 * time.Millisecond
	p := newPacer(gap)
	ctx := context.Background()

	start := time.Now()

	for _, host := range []string{"a.example", "b.example"} {
		if err := p.Wait(ctx, host); err != nil {
			t.Fatal(err)
		}
	}

	if elapsed := time.Since(start); elapsed >= gap {
		t.Errorf("first visits waited %v", elapsed)
	}

	if err := p.Wait(ctx, "a.example"); err != nil {
		t.Fatal(err)
	}

	if elapsed := time.Since(start); elapsed < gap {
		t.Errorf("second visit after %v", elapsed)
	}
}

func TestPacerCancel(t *testing.T) {
	p := newPacer(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())

	if err := p.Wait(ctx, "a.example"); err != nil {
		t.Fatal(err)
	}

	cancel()

	if err := p.Wait(ctx, "a.example"); err == nil {
		t.Error("no error after cancel")
	}
}
