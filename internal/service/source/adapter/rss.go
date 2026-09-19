package adapter

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/dagulv/palantir/internal/service/feed"
	"github.com/mmcdole/gofeed"
)

// var _ Adapter = rss{}

type rss struct {
	client *http.Client
	fp     *gofeed.Parser
}

func NewRSS() Adapter {
	client := &http.Client{
		Timeout: time.Second * 20,
	}

	fp := gofeed.NewParser()

	return rss{
		client: client,
		fp:     fp,
	}
}

func (r rss) Find(ctx context.Context, url string) (urls []string, err error) {
	fmt.Println("finding feeds")
	return
}
func (r rss) Fetch(ctx context.Context, url string) (data []byte, err error) {
	fmt.Println("fetching feed")
	return
}
func (r rss) Process(ctx context.Context, data []byte) (out feed.Feed, err error) {
	fmt.Println("processing feed")
	return
}
