package source

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
)

func TestRetryBatch(t *testing.T) {
	c := &fakeClassifier{err: port.ErrUnavailable}
	s, base, db := qualityServiceDB(t, c)
	enriched := make(chan string, 10)
	s.Enriched = enriched
	ctx := context.Background()
	l := slog.New(slog.DiscardHandler)

	var ids []string
	for _, url := range []string{"https://a.example/1", "https://a.example/2"} {
		base.Content = url + " " + article

		p, _ := insertAndCheck(t, s, base, url)
		ids = append(ids, p.ID)
	}

	if _, err := db.Exec(ctx, `UPDATE raw_posts SET created_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}

	c.calls = 0

	if err := s.retryBatch(ctx, l); err != nil {
		t.Fatal(err)
	}

	for _, id := range ids {
		if p, err := s.RawPostStore.GetRawPost(ctx, id); err != nil || p.Status != domain.StatusPending {
			t.Errorf("outage: status %q, %v", p.Status, err)
		}
	}

	if c.calls != 1 {
		t.Errorf("outage: %d calls", c.calls)
	}

	c.err, c.calls = errors.New("jev: 400 Bad Request"), 0

	if err := s.retryBatch(ctx, l); err != nil {
		t.Fatal(err)
	}

	if c.calls != 2 {
		t.Errorf("failed posts: %d calls", c.calls)
	}

	c.err, c.score = nil, 0.9

	if err := s.retryBatch(ctx, l); err != nil {
		t.Fatal(err)
	}

	for _, id := range ids {
		if p, err := s.RawPostStore.GetRawPost(ctx, id); err != nil || p.Status == domain.StatusPending {
			t.Errorf("status %q, %v", p.Status, err)
		}
	}

	if len(enriched) == 0 {
		t.Error("sent no ids")
	}
}
