package source

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

type fakeClassifier struct {
	score     float64
	err       error
	calls     int
	state     map[string]string
	questions map[string]port.NoulQuestion
	l         *slog.Logger
}

func (f *fakeClassifier) Noul(_ context.Context, l *slog.Logger, state any, questions map[string]port.NoulQuestion) (map[string]float64, error) {
	f.calls++
	f.l = l
	f.state = state.(map[string]string)
	f.questions = questions

	return map[string]float64{"article": f.score}, f.err
}

func TestIsArticle(t *testing.T) {
	for _, score := range []float64{0.8, 0.95, 0.79, 0} {
		c := &fakeClassifier{score: score}

		got, err := isArticle(context.Background(), c, slog.New(slog.DiscardHandler), "Volvo köper Northvolt", "text")
		if err != nil || got != score {
			t.Errorf("%v: got %v, %v", score, got, err)
		}
	}
}

func TestIsArticleRequest(t *testing.T) {
	c := &fakeClassifier{score: 1}

	if _, err := isArticle(context.Background(), c, slog.New(slog.DiscardHandler), "Title", strings.Repeat("å", 70_000)); err != nil {
		t.Fatal(err)
	}

	if c.state["title"] != "Title" || len([]rune(c.state["article"])) != 60_000 {
		t.Errorf("state title %q, article runes %d", c.state["title"], len([]rune(c.state["article"])))
	}

	if q, ok := c.questions["article"]; !ok || q.Criteria == nil || q.Instructions == "" {
		t.Errorf("questions %+v", c.questions)
	}
}

func TestIsArticleError(t *testing.T) {
	c := &fakeClassifier{err: port.ErrUnavailable}

	if _, err := isArticle(context.Background(), c, slog.New(slog.DiscardHandler), "t", "a"); !errors.Is(err, port.ErrUnavailable) {
		t.Errorf("err %v", err)
	}
}

func qualityService(t *testing.T, c port.Classifier) (*Service, domain.RawPost) {
	s, p, _ := qualityServiceDB(t, c)
	return s, p
}

func qualityServiceDB(t *testing.T, c port.Classifier) (*Service, domain.RawPost, *database.Database) {
	db := dbtest.New(t)
	ctx := context.Background()

	var p domain.RawPost

	if err := db.QueryRow(ctx, `INSERT INTO domains (base_url) VALUES ('https://a.example') RETURNING id`).Scan(&p.DomainID); err != nil {
		t.Fatal(err)
	}

	if err := db.QueryRow(ctx, `INSERT INTO sources (domain_id, adapter, url) VALUES ($1, 'rss', 'https://a.example/feed') RETURNING id`, p.DomainID).Scan(&p.SourceID); err != nil {
		t.Fatal(err)
	}

	sources := postgres.NewSourceStore(db)
	domains := postgres.NewStore(db)
	rawPosts := postgres.NewRawPostStore(db)
	s := NewService(&sources, &domains, &rawPosts, nil, c, nil, make(chan string, 10), schedule.Always, nil, slog.New(slog.DiscardHandler))

	p.Lang = "sv"
	p.Title = "Volvo köper Northvolt"
	p.Content = article

	return &s, p, db
}

func insertAndCheck(t *testing.T, s *Service, p domain.RawPost, url string) (domain.RawPost, error) {
	t.Helper()

	ctx := context.Background()
	l := slog.New(slog.DiscardHandler)

	p.URL = url
	p, _ = SetSimhash(ctx, l, p)

	p, err := s.claim(ctx, l, p)
	if err != nil {
		t.Fatalf("err claim: %v", err)
	}

	if p, err = s.saveContent(ctx, l, p); err != nil {
		t.Fatalf("err save content: %v", err)
	}

	return s.checkQuality(ctx, l, p)
}

func TestCheckQuality(t *testing.T) {
	c := &fakeClassifier{score: 0.9}
	s, base := qualityService(t, c)

	p, err := insertAndCheck(t, s, base, "https://a.example/1")
	if err != nil || p.Status != domain.StatusAccepted {
		t.Errorf("first: status %q, %v", p.Status, err)
	}

	p, err = insertAndCheck(t, s, base, "https://a.example/copy")
	if err != nil || p.Status != domain.StatusDuplicate {
		t.Errorf("copy: status %q, %v", p.Status, err)
	}

	c.score = 0.2
	base.Content = otherArticle

	p, err = insertAndCheck(t, s, base, "https://a.example/junk")
	if err != nil || p.Status != domain.StatusRejected {
		t.Errorf("junk: status %q, %v", p.Status, err)
	}
}

func TestCheckQualityJevError(t *testing.T) {
	s, base := qualityService(t, &fakeClassifier{err: errors.New("jev 500")})

	if p, err := insertAndCheck(t, s, base, "https://a.example/1"); !errors.Is(err, pipeline.ErrSkip) || p.Status != domain.StatusPending {
		t.Errorf("status %q, %v", p.Status, err)
	}
}

func TestCheckQualityNoClassifier(t *testing.T) {
	s, base := qualityService(t, nil)

	if p, err := insertAndCheck(t, s, base, "https://a.example/1"); err != nil || p.Status != domain.StatusAccepted {
		t.Errorf("status %q, %v", p.Status, err)
	}
}

func TestInsertExisting(t *testing.T) {
	s, base := qualityService(t, nil)
	ctx := context.Background()
	l := slog.New(slog.DiscardHandler)

	base.URL = "https://a.example/1"

	if _, err := s.insert(ctx, l, base); err != nil {
		t.Fatal(err)
	}

	if _, err := s.insert(ctx, l, base); !errors.Is(err, pipeline.ErrSkip) {
		t.Errorf("err %v", err)
	}
}

func TestSaveStatusSendsAccepted(t *testing.T) {
	enriched := make(chan string, 10)
	s, base := qualityService(t, nil)
	s.Enriched = enriched
	ctx := context.Background()
	l := slog.New(slog.DiscardHandler)

	for _, c := range []struct{ url, status string }{{"https://a.example/1", domain.StatusAccepted}, {"https://a.example/2", domain.StatusRejected}} {
		base.URL = c.url

		p, err := s.insert(ctx, l, base)
		if err != nil {
			t.Fatal(err)
		}

		p.Status = c.status

		if err := s.saveStatus(ctx, l, p); err != nil {
			t.Fatal(err)
		}
	}

	if len(enriched) != 1 {
		t.Fatalf("sent %d ids", len(enriched))
	}
}

func TestSaveStatusQueueFull(t *testing.T) {
	s, base := qualityService(t, nil)
	s.Enriched = make(chan string)
	ctx := context.Background()

	var logged bytes.Buffer
	l := slog.New(slog.NewTextHandler(&logged, nil))

	base.URL = "https://a.example/1"

	p, err := s.insert(ctx, l, base)
	if err != nil {
		t.Fatal(err)
	}

	p.Status = domain.StatusAccepted

	if err := s.saveStatus(ctx, l, p); err != nil {
		t.Errorf("err %v", err)
	}

	if !strings.Contains(logged.String(), "level=WARN") || !strings.Contains(logged.String(), "enrich queue full") {
		t.Errorf("log %q", logged.String())
	}
}

func TestQualitySteps(t *testing.T) {
	cases := []struct {
		name       string
		classifier *fakeClassifier
		status     string
		outcome    string
		reason     string
	}{
		{"rejected", &fakeClassifier{score: 0.12}, domain.StatusRejected, domain.OutcomeStopped, "score 0.12, needs 0.80"},
		{"accepted", &fakeClassifier{score: 0.9}, domain.StatusAccepted, domain.OutcomePassed, ""},
		{"jev down", &fakeClassifier{err: port.ErrUnavailable}, domain.StatusPending, domain.OutcomeError, ""},
		{"no classifier", nil, domain.StatusAccepted, domain.OutcomePassed, "quality check off"},
	}

	for _, c := range cases {
		var classifier port.Classifier
		if c.classifier != nil {
			classifier = c.classifier
		}

		s, base := qualityService(t, classifier)
		rec := newCapture()
		s.Calls = slog.New(rec)

		p, _ := insertAndCheck(t, s, base, "https://a.example/1")

		if p.Status != c.status {
			t.Errorf("%s: status %q", c.name, p.Status)
		}

		if got, want := rec.stepLine(p.ID), "duplicate_check:passed quality_check:"+c.outcome; got != want {
			t.Errorf("%s: steps %q, want %q", c.name, got, want)
		}

		last := rec.last(p.ID)

		if c.reason != "" && last.attrs["reason"] != c.reason {
			t.Errorf("%s: reason %q", c.name, last.attrs["reason"])
		}

		if c.outcome == domain.OutcomeError && last.attrs["reason"] == "" {
			t.Errorf("%s: error step without a reason", c.name)
		}
	}
}

func TestQualityStepsDuplicate(t *testing.T) {
	s, base := qualityService(t, &fakeClassifier{score: 0.9})
	rec := newCapture()
	s.Calls = slog.New(rec)

	insertAndCheck(t, s, base, "https://a.example/1")
	p, _ := insertAndCheck(t, s, base, "https://a.example/copy")

	if p.Status != domain.StatusDuplicate || rec.stepLine(p.ID) != "duplicate_check:stopped" {
		t.Errorf("status %q, steps %q", p.Status, rec.stepLine(p.ID))
	}
}

func TestQualityCallsCarryRawPostID(t *testing.T) {
	c := &fakeClassifier{score: 0.9}
	s, base := qualityService(t, c)
	rec := newCapture()
	s.Calls = slog.New(rec)

	p, _ := insertAndCheck(t, s, base, "https://a.example/1")

	c.l.Info("probe")

	got := (*rec.records)[len(*rec.records)-1]

	if got.msg != "probe" || got.attrs["raw_post_id"] != p.ID || got.attrs["step"] != domain.StepQualityCheck {
		t.Errorf("record %+v", got)
	}
}

func TestStepWriteFailureKeepsOutcome(t *testing.T) {
	s, base := qualityService(t, &fakeClassifier{score: 0.9})
	s.Calls = slog.New(failing{})

	if p, err := insertAndCheck(t, s, base, "https://a.example/1"); err != nil || p.Status != domain.StatusAccepted {
		t.Errorf("status %q, %v", p.Status, err)
	}
}
