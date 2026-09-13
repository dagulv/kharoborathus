package adapter

import (
	"context"
	"net/http"

	"github.com/dagulv/palantir/internal/source"
	"github.com/mmcdole/gofeed"
)

var _ source.Source = rss{}

type rss struct {
	db     string
	client *http.Client
	parser *gofeed.Parser
}

func NewRSS() source.Source {
	return rss{}
}

func (r rss) Fetch(ctx context.Context) (err error) {
	return
}
func (r rss) Process(ctx context.Context) (err error) {
	return
}
func (r rss) Publish(ctx context.Context) (err error) {
	return
}
