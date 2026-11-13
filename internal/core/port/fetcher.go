package port

import (
	"context"
	"errors"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

// A source adapter. It skips posts already stored and sends each post once its article is fetched.
// l takes the third-party request logs.
type Fetcher interface {
	Fetch(ctx context.Context, l *slog.Logger, url string) (<-chan domain.RawPost, error)
}

var ErrNoArchive = errors.New("no archive")

// A Fetcher that can also read a source's history. ErrNoArchive when it finds none.
type Backfiller interface {
	Fetcher
	Backfill(ctx context.Context, l *slog.Logger, url string) (<-chan domain.RawPost, error)
}

type Prober interface {
	Probe(ctx context.Context, url string) (domain.Listing, error)
}
