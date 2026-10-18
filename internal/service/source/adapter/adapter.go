package adapter

import (
	"context"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/service/feed"
)

type Adapter[T any] interface {
	FetchFromSource(ctx context.Context, l *slog.Logger, url string) (<-chan T, error)
	Filter(ctx context.Context, data T) (bool, error)
	FetchContent(ctx context.Context, l *slog.Logger, data T) (T, error)
	ProcessFeed(ctx context.Context, data T) (feed.Feed, error)
}
