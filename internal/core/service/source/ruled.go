package source

import (
	"context"
	"database/sql"
	"iter"
	"log/slog"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

type sourceLister interface {
	ListSources(ctx context.Context, filter domain.SourceFilter) iter.Seq2[domain.Source, error]
}

// BackfillRuled backfills a domain's page sources once a rule they need is saved.
// A page backfill without a links rule finds nothing, so a new page source starts here.
func BackfillRuled(ctx context.Context, s sourceLister, ruled <-chan string, backfills chan<- domain.Source, l *slog.Logger) {
	for {
		var domainID string

		select {
		case id, ok := <-ruled:
			if !ok {
				return
			}

			domainID = id
		case <-ctx.Done():
			return
		}

		for source, err := range s.ListSources(ctx, domain.SourceFilter{
			DomainID: sql.Null[string]{V: domainID, Valid: true},
			Adapter:  sql.Null[string]{V: "page", Valid: true},
			Enabled:  sql.Null[bool]{V: true, Valid: true},
			Status:   sql.Null[string]{V: domain.StatusActive, Valid: true},
		}) {
			if err != nil {
				l.Error("list ruled sources", "domain_id", domainID, "err", err)
				break
			}

			select {
			case backfills <- source:
			default:
				l.Warn("backfill queue full", "source", source.ID)
			}
		}
	}
}
