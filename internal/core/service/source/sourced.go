package source

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
	"github.com/jackc/pgx/v5"
)

const (
	DomainPace = time.Second * 5
	workers    = 5
)

type Fetcher interface {
	Plugin(ctx context.Context) pipeline.Pipeline[domain.Source, domain.RawPost]
}

type fetcher[T any] struct {
	port.Fetcher[T]

	calls *slog.Logger
}

// Keyed by domain.Source.Adapter.
func Run(ctx context.Context, s *Service, fetchers map[string]Fetcher, l *slog.Logger) error {
	ins := make(map[string]chan domain.Source, len(fetchers))
	posts := make([]<-chan domain.RawPost, 0, len(fetchers))

	for name, f := range fetchers {
		in := make(chan domain.Source)
		ins[name] = in
		posts = append(posts, f.Plugin(ctx).Stream(ctx, l.With("adapter", name), in))
	}

	go route(ctx, s.sourceStream(ctx, l.WithGroup("sourceStream")), ins, l)

	return pipeline.New[domain.RawPost]().
		Next(workers, s.Extract).
		Next(workers, SetMarkdown).
		WithSink(s.save).
		Run(ctx, l, pipeline.Merge(ctx, posts...))
}

// Closing every input ends each plugin's stream, which ends the merge and so Run.
func route(ctx context.Context, sources <-chan domain.Source, ins map[string]chan domain.Source, l *slog.Logger) {
	defer func() {
		for _, c := range ins {
			close(c)
		}
	}()

	for source := range sources {
		c, ok := ins[source.Adapter]
		if !ok {
			l.Error("unknown adapter", "adapter", source.Adapter, "source", source.ID)
			continue
		}

		select {
		case c <- source:
		case <-ctx.Done():
			return
		}
	}
}

// A url another worker stored first is not a failure.
func (s *Service) save(ctx context.Context, p domain.RawPost) error {
	if _, err := s.RawPostStore.CreateRawPost(ctx, p); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	return nil
}
