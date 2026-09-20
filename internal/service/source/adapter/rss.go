package adapter

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dagulv/palantir/internal/service/feed"
	"github.com/mmcdole/gofeed"
)

// var _ Adapter = rss{}

type rss struct {
	client *http.Client
	fp     *gofeed.Parser
	l      *slog.Logger
}

func NewRSS(l *slog.Logger) Adapter {
	client := &http.Client{
		Timeout: time.Second * 20,
	}

	fp := gofeed.NewParser()

	return rss{
		client: client,
		fp:     fp,
		l:      l,
	}
}

func (r rss) FetchFromSource(ctx context.Context, url string) (data [][]byte, err error) {
	fmt.Println("fetching feed")

	return
}
func (r rss) ProcessFeed(ctx context.Context, data []byte) (out feed.Feed, err error) {
	fmt.Println("processing feed")
	return
}
