package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// ErrSkip drops an item without logging it as a failure.
var ErrSkip = errors.New("skip")

type step[In, Out any] func(context.Context, *slog.Logger, <-chan In) <-chan Out

type Pipeline[In, Out any] struct {
	run  step[In, Out]
	sink func(context.Context, Out) error
}

func New[T any]() Pipeline[T, T] {
	return Pipeline[T, T]{
		run: func(_ context.Context, _ *slog.Logger, in <-chan T) <-chan T { return in },
	}
}

func (p Pipeline[In, Out]) WithSink(sink func(context.Context, Out) error) Pipeline[In, Out] {
	p.sink = sink

	return p
}

func (p Pipeline[In, Out]) Stream(ctx context.Context, l *slog.Logger, in <-chan In) <-chan Out {
	return p.run(ctx, l, in)
}

func (p Pipeline[In, Out]) Run(ctx context.Context, l *slog.Logger, in <-chan In) error {
	for v := range p.run(ctx, l, in) {
		if p.sink == nil {
			continue
		}

		if err := p.sink(ctx, v); err != nil {
			l.Error("pipeline sink", "err", err)
		}
	}

	return ctx.Err()
}

func (p Pipeline[In, Out]) Next[Next any](
	workers int,
	fn func(context.Context, *slog.Logger, Out) (Next, error),
) Pipeline[In, Next] {
	return Pipeline[In, Next]{
		run: func(ctx context.Context, l *slog.Logger, in <-chan In) <-chan Next {
			return fanOut(ctx, workers, p.run(ctx, l, in), l, func(ctx context.Context, l *slog.Logger, in <-chan Out) <-chan Next {
				return handle(ctx, l, in, fn)
			})
		},
	}
}

func (p Pipeline[In, Out]) NextCh[Next any](
	workers int,
	next step[Out, Next],
) Pipeline[In, Next] {
	return Pipeline[In, Next]{
		run: func(ctx context.Context, l *slog.Logger, in <-chan In) <-chan Next {
			return fanOut(ctx, workers, p.run(ctx, l, in), l, next)
		},
	}
}

func handle[In, Out any](
	ctx context.Context,
	l *slog.Logger,
	in <-chan In,
	fn func(context.Context, *slog.Logger, In) (Out, error),
) <-chan Out {
	out := make(chan Out)

	go func() {
		defer close(out)

		for v := range in {
			res, err := fn(ctx, l, v)
			if errors.Is(err, ErrSkip) {
				continue
			}

			if err != nil {
				// Failures caused by shutdown are not item failures.
				if ctx.Err() == nil {
					l.Error("pipeline step", "err", err)
				}
				continue
			}

			select {
			case out <- res:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func fanOut[In, Out any](ctx context.Context, n int, in <-chan In, l *slog.Logger, stage step[In, Out]) <-chan Out {
	outs := make([]<-chan Out, n)

	for i := range outs {
		outs[i] = stage(ctx, l, in)
	}

	return Merge(ctx, outs...)
}

func Merge[T any](ctx context.Context, chans ...<-chan T) <-chan T {
	out := make(chan T)

	var wg sync.WaitGroup
	wg.Add(len(chans))

	for _, c := range chans {
		go func() {
			defer wg.Done()

			for v := range c {
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}
