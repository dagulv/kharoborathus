package logs

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
)

const day = 24 * time.Hour

func insertLog(t *testing.T, db *database.Database, typ string, age time.Duration) {
	t.Helper()

	if _, err := db.Exec(context.Background(), `
		INSERT INTO logs (type, level, request, created_at) VALUES ($1, 'INFO', '{}', $2)
	`, typ, time.Now().Add(-age)); err != nil {
		t.Fatal(err)
	}
}

func remainingTypes(t *testing.T, db *database.Database) []string {
	t.Helper()

	var types []string
	if err := db.QueryRow(context.Background(), `SELECT coalesce(array_agg(type ORDER BY type), '{}') FROM logs`).Scan(&types); err != nil {
		t.Fatal(err)
	}

	return types
}

func TestPruneDeletesOldRows(t *testing.T) {
	db := dbtest.New(t)
	insertLog(t, db, "rss", 31*day)
	insertLog(t, db, "deepseek", 40*day)
	insertLog(t, db, "rss", 29*day)

	n, err := deleteBefore(context.Background(), db, time.Now().Add(-30*day), 10_000)
	if err != nil {
		t.Fatal(err)
	}

	if n != 2 {
		t.Errorf("deleted %d, want 2", n)
	}

	if got := remainingTypes(t, db); !slices.Equal(got, []string{"rss"}) {
		t.Errorf("remaining %v, want [rss]", got)
	}
}

func TestPruneKeepsSteps(t *testing.T) {
	db := dbtest.New(t)
	insertLog(t, db, "step", 90*day)
	insertLog(t, db, "jev", 90*day)

	n, err := deleteBefore(context.Background(), db, time.Now().Add(-30*day), 10_000)
	if err != nil {
		t.Fatal(err)
	}

	if n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}

	if got := remainingTypes(t, db); !slices.Equal(got, []string{"step"}) {
		t.Errorf("remaining %v, want [step]", got)
	}
}

func TestPruneDeletesAllBatches(t *testing.T) {
	db := dbtest.New(t)
	for range 5 {
		insertLog(t, db, "rss", 31*day)
	}

	n, err := deleteBefore(context.Background(), db, time.Now().Add(-30*day), 2)
	if err != nil {
		t.Fatal(err)
	}

	if n != 5 {
		t.Errorf("deleted %d, want 5", n)
	}

	if got := remainingTypes(t, db); len(got) != 0 {
		t.Errorf("remaining %v, want none", got)
	}
}

// Rules are generated once per domain, so a trace links to a rule call that can be months old.
func TestPruneKeepsRuleCalls(t *testing.T) {
	db := dbtest.New(t)
	insertLog(t, db, "deepseek", 90*day)

	if _, err := db.Exec(context.Background(), `
		INSERT INTO logs (type, level, request, metadata, created_at) VALUES ('deepseek', 'INFO', '{}', '{"domain_id": "d"}', $1)
	`, time.Now().Add(-90*day)); err != nil {
		t.Fatal(err)
	}

	n, err := deleteBefore(context.Background(), db, time.Now().Add(-30*day), 10_000)
	if err != nil {
		t.Fatal(err)
	}

	if n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}

	var kept int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM logs WHERE metadata ? 'domain_id'`).Scan(&kept); err != nil {
		t.Fatal(err)
	}

	if kept != 1 {
		t.Errorf("kept %d rule calls, want 1", kept)
	}
}
