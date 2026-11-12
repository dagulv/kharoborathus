package port

import (
	"context"
	"errors"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

type Fetcher[T any] interface {
	FetchFeed(ctx context.Context, l *slog.Logger, url string) (<-chan T, error)
	Filter(ctx context.Context, data T) (bool, error)
	FetchPost(ctx context.Context, l *slog.Logger, data T) (T, error)
	ProcessFeed(ctx context.Context, data T) (domain.RawPost, error)
}

var ErrNoArchive = errors.New("no archive")

// An adapter that can read a source's history. Items run through the adapter's Filter, FetchPost and ProcessFeed.
type Backfiller[T any] interface {
	Backfill(ctx context.Context, l *slog.Logger, url string) (<-chan T, error)
}
