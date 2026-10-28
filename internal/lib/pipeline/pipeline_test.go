package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
)

func TestPipeline(t *testing.T) {
	l := slog.New(slog.DiscardHandler)

	var starts atomic.Int32

	repeat := func(ctx context.Context, l *slog.Logger, in <-chan int) <-chan int {
		starts.Add(1)
		out := make(chan int)

		go func() {
			defer close(out)

			for v := range in {
				for range v {
					out <- v
				}
			}
		}()

		return out
	}

	double := func(_ context.Context, _ *slog.Logger, v int) (int, error) {
		switch v {
		case 2:
			return 0, ErrSkip
		case 3:
			return 0, errors.New("fail")
		}

		return v * 2, nil
	}

	in := make(chan int)

	go func() {
		defer close(in)

		for _, v := range []int{1, 2, 3, 4} {
			in <- v
		}
	}()

	var got []int

	for v := range New[int]().NextCh(3, repeat).Next(2, double).Stream(t.Context(), l, in) {
		got = append(got, v)
	}

	slices.Sort(got)

	if want := []int{2, 8, 8, 8, 8}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	if n := starts.Load(); n != 3 {
		t.Errorf("NextCh started %d workers, want 3", n)
	}
}

func TestSinkErrorContinues(t *testing.T) {
	l := slog.New(slog.DiscardHandler)

	in := make(chan int)

	go func() {
		defer close(in)

		for v := range 3 {
			in <- v
		}
	}()

	var seen atomic.Int32

	sink := func(_ context.Context, v int) error {
		seen.Add(1)

		if v == 0 {
			return errors.New("fail")
		}

		return nil
	}

	if err := New[int]().WithSink(sink).Run(t.Context(), l, in); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if n := seen.Load(); n != 3 {
		t.Errorf("sink saw %d items, want 3", n)
	}
}
