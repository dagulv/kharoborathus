package schedule

import (
	"context"
	"testing"
	"time"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()

	ts, err := time.Parse(time.RFC3339, s)

	if err != nil {
		t.Fatal(err)
	}

	return ts
}

// 2026-09-21 is a monday, 2026-09-19 a saturday.
func TestActive(t *testing.T) {
	s := Schedule{
		Closed: []Window{
			{Days: Weekdays, From: 1 * time.Hour, To: 4 * time.Hour},
			{Days: Weekdays, From: 6 * time.Hour, To: 10 * time.Hour},
		},
		Open: func(t time.Time) bool {
			return t.Month() == time.December && t.Day() == 24
		},
	}

	cases := []struct {
		name   string
		at     string
		active bool
	}{
		{"before the first window", "2026-09-21T00:59:59Z", true},
		{"start of the first window", "2026-09-21T01:00:00Z", false},
		{"end of the first window", "2026-09-21T04:00:00Z", true},
		{"between windows", "2026-09-21T05:30:00Z", true},
		{"end of the second window", "2026-09-21T10:00:00Z", true},
		{"weekend", "2026-09-19T02:00:00Z", true},
		{"open day", "2026-12-24T02:00:00Z", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := s.Active(mustParse(t, c.at)); got != c.active {
				t.Errorf("Active(%s) = %t, want %t", c.at, got, c.active)
			}
		})
	}
}

func TestNext(t *testing.T) {
	s := Schedule{
		Closed: []Window{
			{Days: Weekdays, From: 1 * time.Hour, To: 4 * time.Hour},
			{Days: Weekdays, From: 4 * time.Hour, To: 6 * time.Hour},
		},
	}

	cases := []struct {
		name string
		at   string
		want string
	}{
		{"already active", "2026-09-21T23:00:00Z", "2026-09-21T23:00:00Z"},
		{"inside a window", "2026-09-21T01:30:00Z", "2026-09-21T06:00:00Z"},
		{"inside the adjacent window", "2026-09-21T05:00:00Z", "2026-09-21T06:00:00Z"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := s.Next(mustParse(t, c.at))

			if !got.Equal(mustParse(t, c.want)) {
				t.Errorf("Next(%s) = %s, want %s", c.at, got.Format(time.RFC3339), c.want)
			}
		})
	}
}

func TestTickWhileActive(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	tick := Always.Tick(ctx, time.Millisecond)

	for range 3 {
		select {
		case _, ok := <-tick:
			if !ok {
				t.Fatal("channel closed early")
			}
		case <-time.After(time.Second):
			t.Fatal("no tick")
		}
	}

	cancel()

	for range tick {
	}
}

func TestTickWhileClosed(t *testing.T) {
	s := Schedule{Closed: []Window{{From: 0, To: 24 * time.Hour}}}

	tick := s.Tick(t.Context(), time.Millisecond)

	select {
	case <-tick:
		t.Error("ticked while closed")
	case <-time.After(50 * time.Millisecond):
	}
}
