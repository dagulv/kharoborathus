package postgres

import (
	"context"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
)

type StatsStore struct {
	db *database.Database
}

func NewStatsStore(db *database.Database) StatsStore {
	return StatsStore{
		db: db,
	}
}

func (s *StatsStore) Stats(ctx context.Context) (st domain.Stats, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM sources WHERE deleted_at IS NULL),
			(SELECT count(*) FROM sources WHERE deleted_at IS NULL AND enabled),
			(SELECT count(*) FROM sources WHERE deleted_at IS NULL AND status = 'flagged'),
			(SELECT count(*) FROM posts),
			(SELECT count(*) FROM posts WHERE created_at > now() - interval '24 hours'),
			(SELECT count(*) FROM discovery_search_queries WHERE searched_at > now() - interval '24 hours' AND result IS NOT NULL)
		`).Scan(&st.Sources, &st.EnabledSources, &st.FlaggedSources, &st.Posts, &st.PostsToday, &st.SearchesToday); err != nil {
		return st, fmt.Errorf("err get stats: %w", err)
	}

	rows, err := s.db.Query(ctx, `SELECT status, count(*) FROM discovery_posts GROUP BY status`)
	if err != nil {
		return st, fmt.Errorf("err get link counts: %w", err)
	}
	defer rows.Close()

	st.Links = map[string]int{}

	for rows.Next() {
		var status string
		var n int
		if err = rows.Scan(&status, &n); err != nil {
			return st, fmt.Errorf("err scan link count: %w", err)
		}

		st.Links[status] = n
	}

	if err = rows.Err(); err != nil {
		return st, fmt.Errorf("err read link counts: %w", err)
	}

	return st, nil
}
