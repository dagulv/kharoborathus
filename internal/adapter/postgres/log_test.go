package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/jackc/pgx/v5"
)

func TestListLogs(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	if _, err := db.Exec(ctx, `
		INSERT INTO logs (type, level, request, status, duration_ms)
		SELECT 'rss', 'INFO', jsonb_build_object('method', 'GET', 'url', 'https://a.example/' || i), 200, i FROM generate_series(1, 52) i;
		INSERT INTO logs (type, level, request, error, metadata)
		VALUES ('jev', 'ERROR', '{"method":"POST","url":"https://jev"}', 'boom', '{"source":"abc","nested":{"k":"v"}}');
		`); err != nil {
		t.Fatal(err)
	}

	s := NewLogStore(db)

	cases := map[string]struct {
		filter  domain.LogFilter
		want    int
		hasNext bool
	}{
		"rss page 1":     {domain.LogFilter{Type: "rss", Page: 1}, 50, true},
		"rss page 2":     {domain.LogFilter{Type: "rss", Page: 2}, 2, false},
		"error":          {domain.LogFilter{Level: "ERROR", Page: 1}, 1, false},
		"meta pair":      {domain.LogFilter{MetaKey: "source", MetaValue: "abc", Page: 1}, 1, false},
		"meta key":       {domain.LogFilter{MetaKey: "source", Page: 1}, 1, false},
		"meta quote":     {domain.LogFilter{MetaKey: "source", MetaValue: `x"y`, Page: 1}, 0, false},
		"far page":       {domain.LogFilter{Page: 9}, 0, false},
		"type and level": {domain.LogFilter{Type: "rss", Level: "ERROR", Page: 1}, 0, false},
	}

	for name, c := range cases {
		got, err := s.ListLogs(ctx, c.filter)
		if err != nil || len(got.Logs) != c.want || got.HasNext != c.hasNext || got.Logs == nil || got.Page != c.filter.Page {
			t.Errorf("%s: %d logs, next %v, %v", name, len(got.Logs), got.HasNext, err)
		}
	}

	got, err := s.ListLogs(ctx, domain.LogFilter{Level: "ERROR", Page: 1})
	if err != nil || len(got.Logs) != 1 {
		t.Fatal(err)
	}

	l := got.Logs[0]
	if *l.Method != "POST" || *l.URL != "https://jev" || *l.Error != "boom" || l.Status != nil || l.Metadata["source"] != "abc" {
		t.Errorf("fields: %+v", l)
	}

	newest, _ := s.ListLogs(ctx, domain.LogFilter{Page: 1})
	if newest.Logs[0].ID != l.ID {
		t.Errorf("not newest first: %d", newest.Logs[0].ID)
	}
}

func TestGetLog(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	var withBody, noBody int64
	if err := db.QueryRow(ctx, `INSERT INTO logs (type, level, request, response) VALUES ('rss', 'INFO', '{"method":"GET"}', '"<html>"') RETURNING id`).Scan(&withBody); err != nil {
		t.Fatal(err)
	}

	if err := db.QueryRow(ctx, `INSERT INTO logs (type, level, request) VALUES ('rss', 'INFO', '{}') RETURNING id`).Scan(&noBody); err != nil {
		t.Fatal(err)
	}

	s := NewLogStore(db)

	got, err := s.GetLog(ctx, withBody)
	if err != nil || string(got.Request) != `{"method": "GET"}` || string(got.Response) != `"<html>"` || *got.Method != "GET" {
		t.Errorf("with body: %s %s %v", got.Request, got.Response, err)
	}

	got, err = s.GetLog(ctx, noBody)
	if err != nil || got.Response != nil {
		t.Errorf("no body: %s, %v", got.Response, err)
	}

	if _, err := s.GetLog(ctx, 999999); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown: %v", err)
	}
}
