package adapter

import (
	"context"
	"log/slog"

	"github.com/dagulv/palantir/internal/service/feed"
)

type Adapter interface {
	FetchFromSource(ctx context.Context, url string) ([][]byte, error)
	ProcessFeed(ctx context.Context, data []byte) (feed.Feed, error)
}

func InitAdapters(l *slog.Logger) map[string]Adapter {
	return map[string]Adapter{
		"rss": NewRSS(l),
	}
}
