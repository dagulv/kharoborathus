package enrich

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

func age(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.db.Exec(context.Background(), `UPDATE raw_posts SET created_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
}

func TestCatchUp(t *testing.T) {
	f := setup(t)
	raws := []domain.RawPost{
		f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0),
		f.rawPost(t, true, "https://own.example/2", domain.StatusFailed, 1),
	}
	age(t, f)

	if err := f.s.catchUp(context.Background(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}

	for _, r := range raws {
		if _, ok := f.post(t, r.URL); !ok {
			t.Errorf("%s not enriched", r.URL)
		}
	}
}

func TestCatchUpStopsOnError(t *testing.T) {
	f := setup(t)
	f.llm.err = errors.New("deepseek 400")
	first := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)
	second := f.rawPost(t, true, "https://own.example/2", domain.StatusAccepted, 0)
	age(t, f)

	if err := f.s.catchUp(context.Background(), slog.New(slog.DiscardHandler)); err == nil {
		t.Error("no error")
	}

	failed := 0
	for _, r := range []domain.RawPost{first, second} {
		if f.raw(t, r.ID).Status == domain.StatusFailed {
			failed++
		}
	}

	if failed != 1 || f.llm.calls != 1 {
		t.Errorf("failed %d, llm calls %d", failed, f.llm.calls)
	}
}
