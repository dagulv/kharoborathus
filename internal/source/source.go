package source

import "context"

type Source interface {
	Fetch(ctx context.Context) error
	Process(ctx context.Context) error
	Publish(ctx context.Context) error
}
