package adapter

import (
	"context"

	"github.com/dagulv/palantir/internal/service/feed"
)

type Adapter interface {
	Find(ctx context.Context, url string) ([]string, error)
	Fetch(ctx context.Context, url string) ([]byte, error)
	Process(ctx context.Context, data []byte) (feed.Feed, error)
}

var Adapters = map[string]Adapter{
	"rss": NewRSS(),
}
