package plugin

import (
	"context"
	"log/slog"
)

// type Pluginer interface {
// 	Run(ctx context.Context, l *slog.Logger) error
// }

type Plugin struct {
	run func(context.Context, *slog.Logger) error
}

func NewPlugin[In, Out any](in <-chan In, p pipeline[In, Out], sink func(Out) error) *Plugin {

	return &Plugin{run: func(ctx context.Context, log *slog.Logger) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		for v := range p.run(ctx, log, in) {
			if err := sink(v); err != nil {
				return err
			}
		}
		return nil
	}}
}

// type pipeline[In, Out any] func(context.Context, *slog.Logger, <-chan In) <-chan Out

// func (p *Plugin) Run(ctx context.Context) error {
// 	ctx, cancel := context.WithCancel(ctx)
// 	defer cancel()
// 	for v := range p.p(ctx, log, in) {
// 		if err := sink(v); err != nil {
// 			return err
// 		}
// 	}
// 	return ctx.Err()
// }
