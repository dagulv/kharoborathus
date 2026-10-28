package plugin

import (
	"context"
	"log/slog"
	"sync"
)

// type Pipeliner interface {
// 	Next(ctx context.Context, l *slog.Logger) <-chan feed.Feed
// }

type step[In, Out any] func(context.Context, *slog.Logger, <-chan In) <-chan Out
type pipeline[In, Out any] struct {
	run   step[In, Out]
	onErr func(error)
}

func NewPipeline[T any]() pipeline[T, T] {
	return pipeline[T, T]{
		onErr: func(err error) {
			slog.Error("error pipeline: ", err)
		},
		run: func(_ context.Context, _ *slog.Logger, in <-chan T) <-chan T { return in },
	}
}

func (p pipeline[In, Out]) Next[Next any](
	workers int,
	fn func(context.Context, *slog.Logger, Out) (Next, error),
	// pre func(context.Context, Out) error,
	// post func(context.Context, Next) error,
) pipeline[In, Next] {
	return pipeline[In, Next]{
		run: func(ctx context.Context, l *slog.Logger, in <-chan In) <-chan Next {
			return fanOut(ctx, workers, p.run(ctx, l, in), l, func(ctx context.Context, l *slog.Logger, in <-chan Out) <-chan Next {
				// return handle(ctx, l, in, fn, pre, post)
				return handle(ctx, l, in, fn)
			})
		},
	}
}

func (p pipeline[In, Out]) NextCh[Next any](
	workers int,
	next step[Out, Next],
) pipeline[In, Next] {
	return pipeline[In, Next]{
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
	// pre func(context.Context, In) error,
	// post func(context.Context, Out) error,
) <-chan Out {
	out := make(chan Out)

	go func() {
		defer close(out)

		for v := range in {
			// if pre != nil {
			// 	if err := pre(ctx, v); err != nil {
			// 		continue
			// 	}
			// }

			res, err := fn(ctx, l, v)
			if err != nil {
				continue
			}

			// if post != nil {
			// 	if err := post(ctx, res); err != nil {
			// 		continue
			// 	}
			// }

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

	return merge(ctx, outs...)
}

func merge[T any](ctx context.Context, chans ...<-chan T) <-chan T {
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

// func (p pipeline[In, Out]) Next[Next any](f func(Out) Next) pipeline[In, Next] {
// 	return func(ctx context.Context, l *slog.Logger, in <-chan In) <-chan Next {
// 		out := make(chan Next)
// 		go func() {
// 			defer close(out)
// 			for v := range p.run(ctx, l, in) {
// 				select {
// 				case out <- f(v):
// 				case <-ctx.Done():
// 					return
// 				}
// 			}
// 		}()
// 		return out
// 	}
// }
