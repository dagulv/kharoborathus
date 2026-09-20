package adapter

import (
	"context"

	"github.com/dagulv/palantir/internal/service/feed"
)

type Adapter[T any] interface {
	FetchFromSource(ctx context.Context, url string) ([]T, error)
	ProcessFeed(ctx context.Context, data T) (feed.Feed, error)
}
