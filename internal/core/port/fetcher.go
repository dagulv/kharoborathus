package port

import (
	"context"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

type Fetcher[T any] interface {
	FetchFeed(ctx context.Context, l *slog.Logger, url string) (<-chan T, error)
	Filter(ctx context.Context, data T) (bool, error)
	FetchPost(ctx context.Context, l *slog.Logger, data T) (T, error)
	ProcessFeed(ctx context.Context, data T) (domain.RawPost, error)
}
