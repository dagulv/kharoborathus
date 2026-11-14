package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
)

type LogStore struct {
	db *database.Database
}

func NewLogStore(db *database.Database) LogStore {
	return LogStore{
		db: db,
	}
}

const logSummaryColumns = `id, type, level, request->>'method', request->>'url', status, duration_ms, error, metadata, created_at`

func scanLogSummary(l *domain.LogSummary) []any {
	return []any{&l.ID, &l.Type, &l.Level, &l.Method, &l.URL, &l.Status, &l.DurationMs, &l.Error, &l.Metadata, &l.CreatedAt}
}

// Bodies are left out, since a stored page can be megabytes.
// No total count: count(*) over the whole table gets slow as it grows.
func (s *LogStore) ListLogs(ctx context.Context, f domain.LogFilter) (domain.LogPage, error) {
	page := domain.LogPage{Logs: []domain.LogSummary{}, Page: f.Page}
	args := []any{}
	conds := []string{"true"}

	if f.Type != "" {
		args = append(args, f.Type)
		conds = append(conds, fmt.Sprintf(`type = $%d`, len(args)))
	}

	if f.Level != "" {
		args = append(args, f.Level)
		conds = append(conds, fmt.Sprintf(`level = $%d`, len(args)))
	}

	if f.MetaKey != "" && f.MetaValue != "" {
		pair, err := json.Marshal(map[string]string{f.MetaKey: f.MetaValue})
		if err != nil {
			return page, fmt.Errorf("err encode metadata filter: %w", err)
		}

		args = append(args, string(pair))
		conds = append(conds, fmt.Sprintf(`metadata @> $%d::jsonb`, len(args)))
	}

	if f.MetaKey != "" && f.MetaValue == "" {
		args = append(args, f.MetaKey)
		conds = append(conds, fmt.Sprintf(`jsonb_exists(metadata, $%d)`, len(args)))
	}

	const pageSize = 50

	// One extra row tells whether a next page exists.
	args = append(args, pageSize+1, (f.Page-1)*pageSize)

	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM logs WHERE %s ORDER BY id DESC LIMIT $%d OFFSET $%d
		`, logSummaryColumns, strings.Join(conds, " AND "), len(args)-1, len(args)), args...)
	if err != nil {
		return page, fmt.Errorf("err list logs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var l domain.LogSummary
		if err := rows.Scan(scanLogSummary(&l)...); err != nil {
			return page, fmt.Errorf("err scan log: %w", err)
		}

		page.Logs = append(page.Logs, l)
	}

	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("err read logs: %w", err)
	}

	if len(page.Logs) > pageSize {
		page.Logs = page.Logs[:pageSize]
		page.HasNext = true
	}

	return page, nil
}

func (s *LogStore) GetLog(ctx context.Context, id int64) (l domain.LogDetail, err error) {
	dest := append(scanLogSummary(&l.LogSummary), &l.Request, &l.Response)

	if err = s.db.QueryRow(ctx, `SELECT `+logSummaryColumns+`, request, response FROM logs WHERE id = $1`, id).Scan(dest...); err != nil {
		return l, fmt.Errorf("err get log %d: %w", id, err)
	}

	return l, nil
}
