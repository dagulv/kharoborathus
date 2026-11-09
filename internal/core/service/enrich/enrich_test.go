package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	discovery "github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

type fakeLLM struct {
	answer   any
	err      error
	schedule schedule.Schedule
	calls    int
	cancel   context.CancelFunc
}

func (f *fakeLLM) Schedule() schedule.Schedule { return f.schedule }

func (f *fakeLLM) Prompt(context.Context, *slog.Logger, port.Request) (string, error) {
	return "", nil
}

func (f *fakeLLM) Unmarshal(_ context.Context, _ *slog.Logger, _ port.Request, out any) error {
	f.calls++

	if f.cancel != nil {
		f.cancel()
	}

	if f.err != nil {
		return f.err
	}

	b, err := json.Marshal(f.answer)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, out)
}

var closed = schedule.Schedule{Closed: []schedule.Window{{
	Days: []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday},
	To:   24 * time.Hour,
}}}

const article = "Volvo Cars köper Northvolt. [Läs mer](https://news.example/a)"

var goodAnswer = answer{
	Summary: "Volvo Cars köper Northvolt.",
	Tags:    []postgres.Proposal{{Key: "New Factory", Term: "ny fabrik"}, {Key: "acquisition", Term: "förvärv"}},
	Events:  []discovery.Keyword{{Entity: "Volvo Cars", Action: "takeover", Query: "Volvo Cars köper Northvolt"}},
}

type fixture struct {
	db       *database.Database
	s        *Service
	llm      *fakeLLM
	jev      *fakeClassifier
	ids      chan string
	links    chan string
	rawPosts *postgres.RawPostStore
}

func setup(t *testing.T) fixture {
	db := dbtest.New(t)
	ctx := context.Background()

	for _, q := range []string{
		`INSERT INTO tags (key) VALUES ('acquisition')`,
		`INSERT INTO tag_terms (key, lang, term) VALUES ('acquisition', 'sv', 'förvärv')`,
		`INSERT INTO tag_aliases (alias, key) VALUES ('takeover', 'acquisition')`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	rawPosts := postgres.NewRawPostStore(db)
	posts := postgres.NewPostStore(db)
	tags := postgres.NewTagStore(db)

	f := fixture{
		db:       db,
		llm:      &fakeLLM{answer: goodAnswer},
		jev:      &fakeClassifier{scores: map[string]float64{"acquisition": 0.9}},
		ids:      make(chan string, 10),
		links:    make(chan string, 10),
		rawPosts: &rawPosts,
	}
	f.s = &Service{DB: db, RawPosts: &rawPosts, Posts: &posts, Tags: &tags, LLM: f.llm, Classifier: f.jev, SearchIDs: f.ids, DiscoveredIDs: f.links}

	return f
}

func (f fixture) rawPost(t *testing.T, trusted bool, url, status string, retries int) domain.RawPost {
	t.Helper()

	ctx := context.Background()

	var domainID, sourceID string

	if err := f.db.QueryRow(ctx, `INSERT INTO domains (base_url) VALUES ('https://own.example') ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url RETURNING id`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}

	if err := f.db.QueryRow(ctx, `INSERT INTO sources (domain_id, adapter, url, enabled) VALUES ($1, 'rss', 'https://own.example/feed', $2) RETURNING id`, domainID, trusted).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}

	posted := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)

	p, err := f.rawPosts.CreateRawPost(ctx, domain.RawPost{SourceID: sourceID, DomainID: domainID, URL: url, Lang: "sv", Content: article, Title: "Volvo köper", Authors: []string{"TT"}, PostedAt: &posted})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.db.Exec(ctx, `UPDATE raw_posts SET status = $2, retry_count = $3 WHERE id = $1`, p.ID, status, retries); err != nil {
		t.Fatal(err)
	}

	return p
}

func (f fixture) post(t *testing.T, url string) (p domain.Post, found bool) {
	t.Helper()

	err := f.db.QueryRow(context.Background(), `SELECT id, title, short_content, content, tags, authors, posted_at FROM posts WHERE url = $1`, url).
		Scan(&p.ID, &p.Title, &p.ShortContent, &p.Content, &p.Tags, &p.Authors, &p.PostedAt)
	if err != nil {
		return p, false
	}

	return p, true
}

func (f fixture) raw(t *testing.T, id string) domain.RawPost {
	t.Helper()

	p, err := f.rawPosts.GetRawPost(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func enrich(f fixture, id string) error {
	return f.s.Enrich(context.Background(), slog.New(slog.DiscardHandler), id)
}

func TestEnrich(t *testing.T) {
	f := setup(t)
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	p, ok := f.post(t, raw.URL)
	if !ok {
		t.Fatal("no post")
	}

	if p.Title != "Volvo köper" || p.ShortContent != goodAnswer.Summary || p.Content != article || !slices.Equal(p.Authors, []string{"TT"}) || p.PostedAt == nil {
		t.Errorf("post %+v", p)
	}

	if want := []string{"acquisition", "new_factory"}; !slices.Equal(p.Tags, want) {
		t.Errorf("tags %v", p.Tags)
	}

	if len(f.ids) != 1 || len(f.links) != 1 {
		t.Errorf("enqueued %d query ids, %d links", len(f.ids), len(f.links))
	}
}

func TestEnrichEmptySummary(t *testing.T) {
	f := setup(t)
	f.llm.answer = answer{}
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	if p, _ := f.post(t, raw.URL); p.ShortContent != "Volvo Cars köper Northvolt. Läs mer" {
		t.Errorf("short content %q", p.ShortContent)
	}
}

func TestEnrichUntrusted(t *testing.T) {
	f := setup(t)
	raw := f.rawPost(t, false, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	if p, _ := f.post(t, raw.URL); !slices.Equal(p.Tags, []string{"acquisition"}) {
		t.Errorf("tags %v", p.Tags)
	}

	var n int
	if err := f.db.QueryRow(context.Background(), `SELECT count(*) FROM tags WHERE key = 'new_factory'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("untrusted post created %d tags, %v", n, err)
	}
}

func TestEnrichNoClassifier(t *testing.T) {
	f := setup(t)
	f.s.Classifier = nil
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	if p, _ := f.post(t, raw.URL); !slices.Equal(p.Tags, []string{"new_factory"}) {
		t.Errorf("tags %v", p.Tags)
	}
}

func TestEnrichFailures(t *testing.T) {
	cases := map[string]func(f fixture){
		"jev":        func(f fixture) { f.jev.err = errors.New("jev 500") },
		"llm":        func(f fixture) { f.llm.err = port.ErrUnavailable },
		"bad answer": func(f fixture) { f.llm.answer = "not an object" },
	}

	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			breakIt(f)
			raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

			if err := enrich(f, raw.ID); err == nil {
				t.Error("no error")
			}

			if got := f.raw(t, raw.ID); got.Status != domain.StatusFailed || got.RetryCount != 1 {
				t.Errorf("status %q, retries %d", got.Status, got.RetryCount)
			}

			if _, ok := f.post(t, raw.URL); ok {
				t.Error("post created")
			}

			if name == "jev" && f.llm.calls != 0 {
				t.Error("deepseek called after jev failed")
			}
		})
	}
}

func TestEnrichClosedSchedule(t *testing.T) {
	f := setup(t)
	f.llm.schedule = closed
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	if got := f.raw(t, raw.ID); got.Status != domain.StatusAccepted || got.RetryCount != 0 || len(f.jev.requests) != 0 {
		t.Errorf("status %q, retries %d, jev requests %d", got.Status, got.RetryCount, len(f.jev.requests))
	}
}

func TestEnrichCancelled(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.llm.cancel = cancel
	f.llm.err = context.Canceled
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	f.s.Enrich(ctx, slog.New(slog.DiscardHandler), raw.ID)

	if got := f.raw(t, raw.ID); got.Status != domain.StatusAccepted || got.RetryCount != 0 {
		t.Errorf("status %q, retries %d", got.Status, got.RetryCount)
	}
}

func TestEnrichExistingPost(t *testing.T) {
	f := setup(t)
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	<-f.ids
	calls := f.llm.calls

	if err := enrich(f, raw.ID); err != nil || f.llm.calls != calls || len(f.ids) != 0 {
		t.Errorf("err %v, llm calls %d, enqueued %d", err, f.llm.calls-calls, len(f.ids))
	}
}

func TestEnrichRetries(t *testing.T) {
	f := setup(t)
	exhausted := f.rawPost(t, true, "https://own.example/1", domain.StatusFailed, 10)
	retried := f.rawPost(t, true, "https://own.example/2", domain.StatusFailed, 2)
	rejected := f.rawPost(t, true, "https://own.example/3", domain.StatusRejected, 0)

	for _, id := range []string{exhausted.ID, retried.ID, rejected.ID} {
		if err := enrich(f, id); err != nil {
			t.Fatal(err)
		}
	}

	if _, ok := f.post(t, exhausted.URL); ok {
		t.Error("exhausted post enriched")
	}

	if _, ok := f.post(t, retried.URL); !ok {
		t.Error("retried post not enriched")
	}

	if _, ok := f.post(t, rejected.URL); ok {
		t.Error("rejected post enriched")
	}
}

func TestEnrichPeakHours(t *testing.T) {
	f := setup(t)
	f.llm.err = fmt.Errorf("%w: %w: deepseek until later", port.ErrUnavailable, port.ErrPeakHours)
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Errorf("err %v", err)
	}

	if got := f.raw(t, raw.ID); got.Status != domain.StatusAccepted || got.RetryCount != 0 {
		t.Errorf("status %q, retries %d", got.Status, got.RetryCount)
	}
}

func TestEnrichProposalLimits(t *testing.T) {
	f := setup(t)
	f.llm.answer = answer{Summary: "s", Tags: []postgres.Proposal{
		{Key: ""}, {Key: " - "}, {Key: "p1"}, {Key: "p2"}, {Key: "p3"}, {Key: "p4"}, {Key: "p5"}, {Key: "p6"},
	}}
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	if p, _ := f.post(t, raw.URL); !slices.Equal(p.Tags, []string{"acquisition", "p1", "p2", "p3", "p4", "p5"}) {
		t.Errorf("tags %v", p.Tags)
	}

	var n int
	if err := f.db.QueryRow(context.Background(), `SELECT count(*) FROM tags WHERE key IN ('', '_', 'p6')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("bad proposals saved: %d, %v", n, err)
	}
}
