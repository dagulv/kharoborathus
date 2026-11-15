package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type TraceStore struct {
	db *database.Database
}

func NewTraceStore(db *database.Database) TraceStore {
	return TraceStore{
		db: db,
	}
}

// Calls carry no bodies, since a stored page can be megabytes. Steps keep their input and output.
// An id that is not a uuid is reported as not found.
func (s *TraceStore) Trace(ctx context.Context, rawPostID string) (domain.Trace, error) {
	var sourceID, discoveryPostID, queryID sql.NullString
	var domainID, url string

	err := s.db.QueryRow(ctx, `
		SELECT r.source_id, r.discovery_post_id, d.discovery_search_query_id, r.domain_id, r.url
		FROM raw_posts r
		LEFT JOIN discovery_posts d ON d.id = r.discovery_post_id
		WHERE r.id = $1
		`, rawPostID).Scan(&sourceID, &discoveryPostID, &queryID, &domainID, &url)
	if isInvalidText(err) {
		err = pgx.ErrNoRows
	}

	if err != nil {
		return domain.Trace{}, fmt.Errorf("err get traced raw post %s: %w", rawPostID, err)
	}

	tr := domain.Trace{Path: "sourced"}
	if discoveryPostID.Valid {
		tr.Path = "discovered"
	}

	rows, err := s.rows(ctx, rawPostID)
	if err != nil {
		return tr, err
	}

	if len(rows) == 0 && sourceID.Valid {
		return s.legacy(ctx, tr, sourceID.String, url)
	}

	byStep := map[string][]domain.LogSummary{}

	if queryID.Valid {
		if byStep[domain.StepFoundBy], err = s.queryCalls(ctx, queryID.String); err != nil {
			return tr, err
		}
	}

	if byStep[domain.StepDiscover], err = s.discoverCalls(ctx, url); err != nil {
		return tr, err
	}

	tr.Steps = domain.BuildTrace(rows, byStep)

	for i := len(tr.Steps) - 1; i >= 0; i-- {
		if tr.Steps[i].Name != domain.StepExtract {
			continue
		}

		if tr.Steps[i].RuleCall, err = s.ruleCall(ctx, domainID, tr.Steps[i].CreatedAt); err != nil {
			return tr, err
		}

		break
	}

	return tr, nil
}

func (s *TraceStore) rows(ctx context.Context, rawPostID string) ([]domain.LogDetail, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+logSummaryColumns+`,
			CASE WHEN type = 'step' THEN request END,
			CASE WHEN type = 'step' THEN response END
		FROM logs
		WHERE metadata @> jsonb_build_object('raw_post_id', $1::text)
		ORDER BY id
		`, rawPostID)
	if err != nil {
		return nil, fmt.Errorf("err query trace rows: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LogDetail, error) {
		var l domain.LogDetail
		err := row.Scan(append(scanLogSummary(&l.LogSummary), &l.Request, &l.Response)...)

		return l, err
	})
	if err != nil {
		return nil, fmt.Errorf("err scan trace rows: %w", err)
	}

	return out, nil
}

func (s *TraceStore) summaries(ctx context.Context, query string, args ...any) ([]domain.LogSummary, error) {
	rows, err := s.db.Query(ctx, `SELECT `+logSummaryColumns+` FROM logs WHERE `+query+` ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("err query trace calls: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LogSummary, error) {
		var l domain.LogSummary
		err := row.Scan(scanLogSummary(&l)...)

		return l, err
	})
	if err != nil {
		return nil, fmt.Errorf("err scan trace calls: %w", err)
	}

	return out, nil
}

// The search and its result filter, keyed by the query rather than a post.
func (s *TraceStore) queryCalls(ctx context.Context, queryID string) ([]domain.LogSummary, error) {
	return s.summaries(ctx, `metadata @> jsonb_build_object('discovery_search_query_id', $1::text)`, queryID)
}

// One query per search, since the metadata index only serves @>.
func (s *TraceStore) discoverCalls(ctx context.Context, url string) ([]domain.LogSummary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT qp.query_id FROM discovery_search_query_posts qp
		JOIN posts p ON p.id = qp.post_id
		WHERE p.url = $1
		`, url)
	if err != nil {
		return nil, fmt.Errorf("err query post searches: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("err scan post searches: %w", err)
	}

	var out []domain.LogSummary

	for _, id := range ids {
		calls, err := s.queryCalls(ctx, id)
		if err != nil {
			return nil, err
		}

		out = append(out, calls...)
	}

	return out, nil
}

// The latest rule generation for the domain before the extract step ran. A domain-level call has
// no post id, so this is the one link made by domain and time.
func (s *TraceStore) ruleCall(ctx context.Context, domainID string, before time.Time) (*domain.LogSummary, error) {
	calls, err := s.summaries(ctx, `
		id = (
			SELECT id FROM logs
			WHERE type = 'deepseek' AND metadata @> jsonb_build_object('domain_id', $1::text) AND created_at <= $2
			ORDER BY id DESC
			LIMIT 1
		)`, domainID, before)
	if err != nil || len(calls) == 0 {
		return nil, err
	}

	return &calls[0], nil
}

// Posts traced before steps were logged show their fetch, found by source and url.
func (s *TraceStore) legacy(ctx context.Context, tr domain.Trace, sourceID, url string) (domain.Trace, error) {
	calls, err := s.summaries(ctx, `metadata @> jsonb_build_object('source_id', $1::text) AND request->>'url' = $2`, sourceID, url)
	if err != nil {
		return tr, err
	}

	tr.Steps = []domain.TraceStep{{
		Step:  domain.Step{Name: domain.StepFetch, Outcome: domain.OutcomePassed, Reason: "recorded before step tracing"},
		Calls: calls,
	}}

	if calls == nil {
		tr.Steps[0].Calls = []domain.LogSummary{}
	}

	return tr, nil
}
