package source

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/core/domain"
)

// stored inserts a raw post a feed sync already finished with status.
func (f discoveredFixture) stored(t *testing.T, url, status string) string {
	t.Helper()

	var id string
	if err := f.db.QueryRow(context.Background(), `
		INSERT INTO raw_posts (source_id, url, lang, domain_id, status, content)
		SELECT id, $1, 'sv', domain_id, $2, '{"content": "old", "title": "Old title"}' FROM sources LIMIT 1
		RETURNING id
		`, url, status).Scan(&id); err != nil {
		t.Fatal(err)
	}

	return id
}

func (f discoveredFixture) resync(t *testing.T, id string, override bool) domain.RawPost {
	t.Helper()

	if err := f.d.Resync(context.Background(), slog.New(slog.DiscardHandler), domain.Resync{RawPostID: id, Override: override}); err != nil {
		t.Fatal(err)
	}

	p, err := f.d.Service.RawPostStore.GetRawPost(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func TestResyncSourced(t *testing.T) {
	f := discoveredSetup(t)
	id := f.stored(t, pageServer(t).URL+"/a", domain.StatusRejected)

	p := f.resync(t, id, false)

	if p.Status != domain.StatusAccepted || p.Content != article || p.Title != "Old title" {
		t.Errorf("status %q, title %q, content %q", p.Status, p.Title, p.Content)
	}

	if want := "resync:passed fetch:passed extract:passed duplicate_check:passed quality_check:passed"; f.rec.stepLine(id) != want {
		t.Errorf("steps %q, want %q", f.rec.stepLine(id), want)
	}

	if len(f.enriched) != 1 {
		t.Errorf("%d enqueued for enrich", len(f.enriched))
	}
}

func TestResyncOverride(t *testing.T) {
	f := discoveredSetup(t)
	jev := &fakeClassifier{score: 0.1}
	f.d.Service.Classifier = jev
	id := f.stored(t, pageServer(t).URL+"/a", domain.StatusRejected)

	if p := f.resync(t, id, false); p.Status != domain.StatusRejected || jev.calls != 1 {
		t.Fatalf("without override: status %q, jev calls %d", p.Status, jev.calls)
	}

	p := f.resync(t, id, true)
	last := f.rec.last(id)

	if p.Status != domain.StatusAccepted || jev.calls != 1 || last.attrs["step"] != domain.StepQualityCheck || last.attrs["reason"] != "manual override" {
		t.Errorf("status %q, jev calls %d, last step %v %v", p.Status, jev.calls, last.attrs["step"], last.attrs["reason"])
	}
}

func TestResyncDiscoveredOverride(t *testing.T) {
	f := discoveredSetup(t)
	f.ex.postedAt = time.Now().AddDate(0, 0, -30)
	url := pageServer(t).URL + "/a"

	if err := f.d.ingest(context.Background(), slog.New(slog.DiscardHandler), f.queue(t, url)); err != nil {
		t.Fatal(err)
	}

	id := f.rawPostID(t, url)

	if p := f.resync(t, id, false); p.Status != domain.StatusRejected {
		t.Fatalf("without override: status %q", p.Status)
	}

	p := f.resync(t, id, true)

	run := "resync:passed domain_check:passed fetch:passed extract:passed date_window:"
	want := "found_by:passed domain_check:passed fetch:passed extract:passed date_window:stopped " +
		run + "stopped " +
		run + "passed source_match:passed duplicate_check:passed quality_check:passed"

	if p.Status != domain.StatusAccepted || p.SourceID == "" || f.rec.stepLine(id) != want {
		t.Errorf("status %q, source %q, steps %q, want %q", p.Status, p.SourceID, f.rec.stepLine(id), want)
	}
}

func TestResyncInFlight(t *testing.T) {
	f := discoveredSetup(t)
	id := f.stored(t, pageServer(t).URL+"/a", domain.StatusPending)

	if p := f.resync(t, id, false); p.Status != domain.StatusPending || f.rec.stepLine(id) != "" {
		t.Errorf("status %q, steps %q", p.Status, f.rec.stepLine(id))
	}
}

// The old content is cleared, so a post whose page is gone cannot be enriched from it.
func TestResyncFetchFailed(t *testing.T) {
	f := discoveredSetup(t)
	id := f.stored(t, pageServer(t).URL+"/missing", domain.StatusAccepted)

	p := f.resync(t, id, false)

	if p.Status != domain.StatusFailed || p.Content != "" || f.rec.stepLine(id) != "resync:passed fetch:stopped" {
		t.Errorf("status %q, content %q, steps %q", p.Status, p.Content, f.rec.stepLine(id))
	}
}
