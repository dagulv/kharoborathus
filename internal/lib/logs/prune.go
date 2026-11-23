package logs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

const (
	retention  = 30 * 24 * time.Hour
	pruneBatch = 10_000
)

// Prune deletes rows older than the retention once at start, then daily.
// Step rows and domain rule calls stay, since post traces link to them and they are few or small.
func Prune(ctx context.Context, db *database.Database, l *slog.Logger) {
	for range schedule.Always.Tick(ctx, 24*time.Hour) {
		n, err := deleteBefore(ctx, db, time.Now().Add(-retention), pruneBatch)
		if err != nil {
			if ctx.Err() == nil {
				l.Error("prune logs", "err", err)
			}

			continue
		}

		l.Info("prune logs", "deleted", n)
	}
}

// Batched, so a large backlog never holds one long transaction.
func deleteBefore(ctx context.Context, db *database.Database, before time.Time, batch int) (int64, error) {
	var total int64

	for {
		tag, err := db.Exec(ctx, `
			DELETE FROM logs WHERE id IN (
				SELECT id FROM logs
				WHERE created_at < $1 AND type <> 'step' AND NOT (type = 'deepseek' AND metadata ? 'domain_id')
				ORDER BY id LIMIT $2
			)
		`, before, batch)
		if err != nil {
			return total, fmt.Errorf("err delete logs: %w", err)
		}

		total += tag.RowsAffected()

		if tag.RowsAffected() < int64(batch) {
			return total, nil
		}
	}
}
