package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/jackc/pgx/v5"
)

func seedLog(t *testing.T, db *database.Database, typ, metadata, request, ago string) int64 {
	t.Helper()

	var id int64
	if err := db.QueryRow(context.Background(), `
		INSERT INTO logs (type, level, request, response, metadata, created_at)
		VALUES ($1, 'INFO', $3::jsonb, '"body"', $2::jsonb, now() - $4::interval)
		RETURNING id
		`, typ, metadata, request, ago).Scan(&id); err != nil {
		t.Fatal(err)
	}

	return id
}

func TestTrace(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	domainID, sourceID := seedSource(t, db, "https://a.example", "https://a.example/feed")
	id := seedRawPost(t, db, sourceID, domainID, "https://a.example/1", "rejected", `{}`, "0")
	other := seedRawPost(t, db, sourceID, domainID, "https://a.example/2", "accepted", `{}`, "0")

	meta := func(rawPostID, step, extra string) string {
		return `{"raw_post_id": "` + rawPostID + `", "step": "` + step + `"` + extra + `}`
	}

	rule := seedLog(t, db, "deepseek", `{"domain_id": "`+domainID+`"}`, `{}`, "10 minutes")
	fetch := seedLog(t, db, "page", meta(id, "fetch", ""), `{"url": "https://a.example/1"}`, "5 minutes")
	seedLog(t, db, "step", meta(id, "fetch", `, "outcome": "passed", "reason": ""`), `{"url": "https://a.example/1"}`, "5 minutes")
	seedLog(t, db, "step", meta(id, "extract", `, "outcome": "passed", "reason": ""`), `{"bytes": 10}`, "4 minutes")
	seedLog(t, db, "page", meta(other, "fetch", ""), `{}`, "4 minutes")
	jev := seedLog(t, db, "jev", meta(id, "quality_check", ""), `{}`, "3 minutes")
	seedLog(t, db, "step", meta(id, "quality_check", `, "outcome": "stopped", "reason": "score 0.12, needs 0.80"`), `{}`, "3 minutes")
	seedLog(t, db, "deepseek", `{"domain_id": "`+domainID+`"}`, `{}`, "1 minute")

	s := NewTraceStore(db)

	tr, err := s.Trace(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	if tr.Path != "sourced" || len(tr.Steps) != 3 {
		t.Fatalf("trace %+v", tr)
	}

	fetchStep, extract, quality := tr.Steps[0], tr.Steps[1], tr.Steps[2]

	if fetchStep.Name != "fetch" || len(fetchStep.Calls) != 1 || fetchStep.Calls[0].ID != fetch {
		t.Errorf("fetch %+v", fetchStep)
	}

	if extract.Name != "extract" || len(extract.Calls) != 0 || extract.RuleCall == nil || extract.RuleCall.ID != rule || string(extract.Input) != `{"bytes": 10}` {
		t.Errorf("extract %+v, rule %+v", extract, extract.RuleCall)
	}

	if quality.Outcome != "stopped" || quality.Reason != "score 0.12, needs 0.80" || len(quality.Calls) != 1 || quality.Calls[0].ID != jev {
		t.Errorf("quality %+v", quality)
	}

	for _, bad := range []string{"00000000-0000-0000-0000-000000000000", "x"} {
		if _, err := s.Trace(ctx, bad); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestTraceLegacy(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	domainID, sourceID := seedSource(t, db, "https://a.example", "https://a.example/feed")
	first := seedRawPost(t, db, sourceID, domainID, "https://a.example/1", "accepted", `{}`, "0")
	second := seedRawPost(t, db, sourceID, domainID, "https://a.example/2", "accepted", `{}`, "0")

	one := seedLog(t, db, "page", `{"source_id": "`+sourceID+`"}`, `{"url": "https://a.example/1"}`, "1 minute")
	two := seedLog(t, db, "page", `{"source_id": "`+sourceID+`"}`, `{"url": "https://a.example/2"}`, "1 minute")

	s := NewTraceStore(db)

	for id, want := range map[string]int64{first: one, second: two} {
		tr, err := s.Trace(ctx, id)
		if err != nil {
			t.Fatal(err)
		}

		if len(tr.Steps) != 1 || tr.Steps[0].Name != "fetch" || tr.Steps[0].Reason != "recorded before step tracing" || len(tr.Steps[0].Calls) != 1 || tr.Steps[0].Calls[0].ID != want {
			t.Errorf("%s: %+v", id, tr.Steps)
		}
	}
}
