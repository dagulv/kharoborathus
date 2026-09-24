package schedule

import (
	"context"
	"slices"
	"time"
)

var Weekdays = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}

var Always = Schedule{}

type Schedule struct {
	Closed []Window
	Loc    *time.Location
	Open   func(time.Time) bool
}

type Window struct {
	Days []time.Weekday
	From time.Duration
	To   time.Duration
}

func (s Schedule) Active(t time.Time) bool {
	t = t.In(s.loc())

	if s.Open != nil && s.Open(t) {
		return true
	}

	for _, w := range s.Closed {
		if w.contains(t) {
			return false
		}
	}

	return true
}

func (s Schedule) Next(t time.Time) time.Time {
	for range len(s.Closed) + 1 {
		if s.Active(t) {
			return t
		}

		t = s.opens(t)
	}

	return t
}

func (s Schedule) Tick(ctx context.Context, interval time.Duration) <-chan time.Time {
	out := make(chan time.Time)

	go func() {
		defer close(out)

		for {
			now := time.Now()

			if wait := s.Next(now).Sub(now); wait > 0 {
				if !sleep(ctx, wait) {
					return
				}

				continue
			}

			select {
			case out <- now:
			case <-ctx.Done():
				return
			}

			if !sleep(ctx, interval) {
				return
			}
		}
	}()

	return out
}

func (s Schedule) opens(t time.Time) time.Time {
	t = t.In(s.loc())

	for _, w := range s.Closed {
		if w.contains(t) {
			y, m, d := t.Date()

			return time.Date(y, m, d, 0, 0, 0, 0, s.loc()).Add(w.To)
		}
	}

	return t
}

func (s Schedule) loc() *time.Location {
	if s.Loc == nil {
		return time.UTC
	}

	return s.Loc
}

func (w Window) contains(t time.Time) bool {
	if len(w.Days) > 0 && !slices.Contains(w.Days, t.Weekday()) {
		return false
	}

	since := offset(t)

	return since >= w.From && since < w.To
}

func offset(t time.Time) time.Duration {
	h, m, s := t.Clock()

	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
