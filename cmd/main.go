package main

import (
	"context"
	"log"

	"github.com/dagulv/palantir/internal/feed"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := app(ctx); err != nil {
		log.Fatal(err)
	}
}

func app(ctx context.Context) (err error) {
	feed.NewDatabase(ctx, )
}
