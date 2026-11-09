package enrich

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

func TestTagCache(t *testing.T) {
	now := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	c := tagCache{now: func() time.Time { return now }}
	ctx := context.Background()

	loads := map[string]int{}
	load := func(_ context.Context, lang string) (tagSet, error) {
		loads[lang]++
		return tagSet{keys: []string{lang}, active: []domain.Tag{{Key: lang}}}, nil
	}

	for range 3 {
		if set, err := c.get(ctx, "sv", load); err != nil || set.keys[0] != "sv" {
			t.Fatalf("set %+v, %v", set, err)
		}
	}

	if _, err := c.get(ctx, "en", load); err != nil {
		t.Fatal(err)
	}

	if loads["sv"] != 1 || loads["en"] != 1 {
		t.Errorf("loads %v", loads)
	}

	now = now.Add(tagCacheTTL + time.Second)

	if _, err := c.get(ctx, "sv", load); err != nil || loads["sv"] != 2 {
		t.Errorf("after ttl: loads %v, %v", loads, err)
	}
}

func TestTagCacheError(t *testing.T) {
	c := tagCache{now: time.Now}
	ctx := context.Background()
	calls := 0

	load := func(context.Context, string) (tagSet, error) {
		calls++
		return tagSet{}, errors.New("db down")
	}

	for range 2 {
		if _, err := c.get(ctx, "sv", load); err == nil {
			t.Error("no error")
		}
	}

	if calls != 2 {
		t.Errorf("a failed load was cached: calls %d", calls)
	}
}
