package source

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/lib/pipeline"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

type fakeClassifier struct {
	score     float64
	err       error
	state     map[string]string
	questions map[string]port.NoulQuestion
}

func (f *fakeClassifier) Noul(_ context.Context, _ *slog.Logger, state any, questions map[string]port.NoulQuestion) (map[string]float64, error) {
	f.state = state.(map[string]string)
	f.questions = questions

	return map[string]float64{"article": f.score}, f.err
}

func TestIsArticle(t *testing.T) {
	cases := map[float64]bool{0.8: true, 0.95: true, 0.79: false, 0: false}

	for score, want := range cases {
		c := &fakeClassifier{score: score}

		got, err := isArticle(context.Background(), c, slog.New(slog.DiscardHandler), "Volvo köper Northvolt", "text")
		if err != nil || got != want {
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
	s := NewService(&sources, &domains, &rawPosts, nil, c, make(chan string, 10), schedule.Always, nil)

	p.Lang = "sv"
	p.Title = "Volvo köper Northvolt"
	p.Content = article

	return &s, p
}

func insertAndCheck(t *testing.T, s *Service, p domain.RawPost, url string) (domain.RawPost, error) {
	t.Helper()

	ctx := context.Background()
	l := slog.New(slog.DiscardHandler)

	p.URL = url
	p, _ = SetSimhash(ctx, l, p)

	p, err := s.insert(ctx, l, p)
	if err != nil {
		t.Fatalf("err insert: %v", err)
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

		if err := s.saveStatus(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	if len(enriched) != 1 {
		t.Fatalf("sent %d ids", len(enriched))
	}
}
