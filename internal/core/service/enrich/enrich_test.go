package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagulv/kharoborathus/internal/adapter/postgres"
	discovery "github.com/dagulv/kharoborathus/internal/adapter/search"
	"github.com/dagulv/kharoborathus/internal/core/domain"
	"github.com/dagulv/kharoborathus/internal/core/port"
	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/dbtest"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/jackc/pgx/v5"
)

type fakeLLM struct {
	answer   any
	err      error
	schedule schedule.Schedule
	calls    int
	prompt   string
	cancel   context.CancelFunc
	l        *slog.Logger
}

func (f *fakeLLM) Schedule() schedule.Schedule { return f.schedule }

func (f *fakeLLM) Prompt(context.Context, *slog.Logger, port.Request) (string, error) {
	return "", nil
}

func (f *fakeLLM) Unmarshal(_ context.Context, l *slog.Logger, req port.Request, out any) error {
	f.calls++
	f.l = l
	f.prompt = req.Prompt

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
	Summary:  "Volvo Cars köper Northvolt.",
	Tags:     []postgres.Proposal{{Key: "New Factory", Term: "ny fabrik"}, {Key: "acquisition", Term: "förvärv"}},
	Events:   []discovery.Keyword{{Entity: "Volvo Cars", Action: "takeover", Query: "Volvo Cars köper Northvolt"}},
	Entities: []postgres.ExtractedEntity{{Key: "Volvo Cars", Name: "Volvo Cars", Term: "volvo cars", Kind: "company"}},
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
		`INSERT INTO categories (key) VALUES ('deals')`,
		`INSERT INTO category_tags (category, tag) VALUES ('deals', 'acquisition')`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	rawPosts := postgres.NewRawPostStore(db)
	posts := postgres.NewPostStore(db)
	tags := postgres.NewTagStore(db)
	categories := postgres.NewCategoryStore(db)
	entities := postgres.NewEntityStore(db)

	f := fixture{
		db:       db,
		llm:      &fakeLLM{answer: goodAnswer},
		jev:      &fakeClassifier{scores: map[string]float64{"deals": 0.9, "acquisition": 0.9}},
		ids:      make(chan string, 10),
		links:    make(chan string, 10),
		rawPosts: &rawPosts,
	}
	f.s = &Service{DB: db, RawPosts: &rawPosts, Posts: &posts, Tags: &tags, Categories: &categories, Entities: &entities, LLM: f.llm, Classifier: f.jev, SearchIDs: f.ids, DiscoveredIDs: f.links, Calls: slog.New(slog.DiscardHandler)}

	return f
}

// Events only search for active or focused entities.
func (f fixture) activeVolvo(t *testing.T) {
	t.Helper()

	if _, err := f.db.Exec(context.Background(), `INSERT INTO entities (key, name, kind, status) VALUES ('volvo_cars', 'Volvo Cars', 'company', 'active')`); err != nil {
		t.Fatal(err)
	}
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

	// Recent, since discovery starts no search for posts older than its window.
	posted := time.Now().UTC().Truncate(time.Hour)

	p, _, err := f.rawPosts.ClaimRawPost(ctx, postgres.Claim{URL: url, SourceID: sourceID})
	if err != nil {
		t.Fatal(err)
	}

	p.SourceID, p.DomainID, p.Lang, p.Content, p.Title, p.Authors, p.PostedAt = sourceID, domainID, "sv", article, "Volvo köper", []string{"TT"}, &posted

	if _, err := f.rawPosts.SaveContent(ctx, p); err != nil {
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
	f.activeVolvo(t)
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

func TestEnrichCategoryMiss(t *testing.T) {
	f := setup(t)
	f.jev.scores["deals"] = 0
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	if p, _ := f.post(t, raw.URL); !slices.Equal(p.Tags, []string{"new_factory"}) {
		t.Errorf("tags %v", p.Tags)
	}

	if len(f.jev.requests) != 1 {
		t.Errorf("jev requests %d", len(f.jev.requests))
	}
}

func TestEnrichPromotes(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	if _, err := f.db.Exec(ctx, `INSERT INTO tags (key, status) VALUES ('wind', 'proposed')`); err != nil {
		t.Fatal(err)
	}

	for i := range 4 {
		if _, err := f.db.Exec(ctx, `
			WITH d AS (
				INSERT INTO domains (base_url) VALUES ($1) ON CONFLICT (base_url) DO UPDATE SET base_url = EXCLUDED.base_url RETURNING id
			), s AS (
				INSERT INTO sources (domain_id, adapter, url, enabled) SELECT id, 'rss', $1 || '/feed', true FROM d RETURNING id, domain_id
			)
			INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content, tags)
			SELECT id, $2, 'sv', domain_id, 't', 's', 'c', '{wind}' FROM s
			`, fmt.Sprintf("https://seed%d.example", i%3), fmt.Sprintf("https://seed.example/%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	f.jev.scores["wind"] = 0.9
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	var status string

	if err := f.db.QueryRow(ctx, `SELECT status FROM tags WHERE key = 'wind'`).Scan(&status); err != nil || status != domain.StatusActive {
		t.Errorf("status %q, %v", status, err)
	}
}

func TestEnrichProposedQualityTagNotAnEventAction(t *testing.T) {
	f := setup(t)

	if _, err := f.db.Exec(context.Background(), `INSERT INTO tags (key, status) VALUES ('rumor', 'proposed')`); err != nil {
		t.Fatal(err)
	}

	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.llm.prompt, "- Quality tag keys: acquisition\n") {
		t.Errorf("prompt %q", f.llm.prompt)
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

	if err := f.db.QueryRow(context.Background(), `SELECT count(*) FROM entities`).Scan(&n); err != nil || n != 0 {
		t.Errorf("untrusted post created %d entities, %v", n, err)
	}
}

func TestEnrichEntities(t *testing.T) {
	f := setup(t)
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	var key, status string

	err := f.db.QueryRow(context.Background(), `
		SELECT pe.entity, e.status FROM post_entities pe
		JOIN entities e ON e.key = pe.entity
		JOIN posts p ON p.id = pe.post_id
		WHERE p.url = $1
		`, raw.URL).Scan(&key, &status)
	if err != nil || key != "volvo_cars" || status != domain.StatusProposed {
		t.Errorf("entity %q %q, %v", key, status, err)
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
	f.activeVolvo(t)
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

// A resync saves new content after the post, so enrich writes the post again in place.
func TestEnrichResyncedPost(t *testing.T) {
	f := setup(t)
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	before, _ := f.post(t, raw.URL)

	raw.Title = "Volvo köper igen"
	if _, err := f.rawPosts.SaveContent(context.Background(), raw); err != nil {
		t.Fatal(err)
	}

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	after, _ := f.post(t, raw.URL)
	if after.ID != before.ID || after.Title != "Volvo köper igen" {
		t.Errorf("post %s %q, want %s with the new title", after.ID, after.Title, before.ID)
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

func TestEnrichSteps(t *testing.T) {
	f := setup(t)
	rec := newCapture()
	f.s.Calls = slog.New(rec)
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	if got := rec.stepLine(raw.ID); got != "enrich:passed discover:passed" {
		t.Fatalf("steps %q", got)
	}

	p, _ := f.post(t, raw.URL)

	if out, _ := rec.steps(raw.ID)[0].attrs["response"].(map[string]any); out["postId"] != p.ID {
		t.Errorf("enrich output %v, post %s", out, p.ID)
	}

	if err := enrich(f, raw.ID); err != nil || rec.stepLine(raw.ID) != "enrich:passed discover:passed" {
		t.Errorf("existing post: %v, steps %q", err, rec.stepLine(raw.ID))
	}
}

func TestEnrichStepsFailure(t *testing.T) {
	f := setup(t)
	rec := newCapture()
	f.s.Calls = slog.New(rec)
	f.llm.err = errors.New("deepseek: 500")
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	enrich(f, raw.ID)
	enrich(f, raw.ID)

	if got := rec.stepLine(raw.ID); got != "enrich:error enrich:error" {
		t.Errorf("steps %q", got)
	}

	if reason, _ := rec.last(raw.ID).attrs["reason"].(string); reason == "" {
		t.Error("error step without a reason")
	}
}

func TestEnrichCallsCarryRawPostID(t *testing.T) {
	f := setup(t)
	rec := newCapture()
	f.s.Calls = slog.New(rec)
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	for name, l := range map[string]*slog.Logger{"llm": f.llm.l, "jev": f.jev.l} {
		l.Info("probe")

		got := (*rec.records)[len(*rec.records)-1]

		if got.msg != "probe" || got.attrs["raw_post_id"] != raw.ID || got.attrs["step"] != domain.StepEnrich {
			t.Errorf("%s: record %+v", name, got)
		}
	}
}

func TestEnrichProposalCategories(t *testing.T) {
	f := setup(t)

	if _, err := f.db.Exec(context.Background(), `INSERT INTO categories (key) VALUES ('energy'), ('real_estate')`); err != nil {
		t.Fatal(err)
	}

	f.llm.answer = answer{Summary: "s", Tags: []postgres.Proposal{{Key: "solar_park", Term: "solpark", Categories: []string{"Energy ", "real estate", " - "}}}}
	raw := f.rawPost(t, true, "https://own.example/1", domain.StatusAccepted, 0)

	if err := enrich(f, raw.ID); err != nil {
		t.Fatal(err)
	}

	rows, err := f.db.Query(context.Background(), `SELECT category FROM category_tags WHERE tag = 'solar_park' ORDER BY category`)
	if err != nil {
		t.Fatal(err)
	}

	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"energy", "real_estate"}; !slices.Equal(got, want) {
		t.Errorf("categories %v", got)
	}

	if want := "Categories: deals, energy, real_estate"; !strings.Contains(f.llm.prompt, want) {
		t.Errorf("prompt misses %q", want)
	}
}
