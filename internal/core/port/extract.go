package port

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

// Each method owns its field's rule in the domain metadata. Nothing found is a zero value, not an error.
type Extractor interface {
	Links(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte) ([]string, error)
	Content(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) (string, error)
	Title(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) (string, error)
	Authors(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) ([]string, error)
	PostedAt(ctx context.Context, l *slog.Logger, d domain.Domain, url string, page []byte) (time.Time, error)
	// Next gives the url of listing page n, the page after url, or "" when there is none or no rule yet.
	Next(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte, n int) (string, error)
}
