# Phase 5: Search Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** While the `search_discovery` switch is on, search Brave for events from trusted posts whose action is a gate tag, let the LLM keep results about the event, and queue the kept URLs in `link_events` for the phase 4 ingester.

**Architecture:** Event extraction learns the gate tag keys and normalizes actions to tag keys through `tag_aliases`. A new search worker in the `discovery` package reads searchable `discovery_events` items, dedupes events, calls Brave through a `Searcher` interface, filters results with a `robot.ResultFilter` and inserts the kept URLs with `via = 'search'`. Admin shows the switch readiness and daily searches against the budget.

**Tech Stack:** Go 1.27, pgx v5, Postgres 18, SvelteKit admin with postgres.js and valibot.

**Spec:** `docs/kb/search-discovery.md`, `docs/kb/rollout.md` (phase 5, `search_discovery` switch, monitoring).

## Global Constraints

- A search runs only for events whose `action` is the key of a tag with `enabled`, `status = 'active'` and `gate = true`, from trusted posts (source `status` `active` or `dormant`), created inside the 7-day dedupe window.
- Event extraction keeps running on every post. The discovery prompt gets the current gate tag keys. Actions are normalized like tag keys and remapped through `tag_aliases`.
- Dedupe: skip the query and `MarkSearched` when the same lowercased `entity` and `action` were searched inside the dedupe window.
- Brave request: `GET https://api.search.brave.com/res/v1/web/search`, header `X-Subscription-Token`, params `q`, `country=SE`, `search_lang` from the post language `sv` or `en`, `freshness=pw`, `count=20`, `result_filter=web,news`, `extra_snippets=true`. Results from `web.results` and `news.results`, deduplicated by URL.
- The LLM filter sees `title`, `description`, `extra_snippets`, `page_age` and hostname plus the event's `entity` and `action`. Only kept URLs are stored. Search engine URLs never enter `link_events`.
- Kept URLs go into `link_events` with `via = 'search'` and `discovery_events_id`, then `MarkSearched`.
- The worker reads `search_discovery` at the start of every tick and skips the tick when off. `schema.sql` inserts the switch row with `ON CONFLICT DO NOTHING`.
- Defaults: dedupe window 7 days, freshness `pw`, 20 results per result type, monthly budget $50 (about 10k queries at $5 per 1k).
- Schema changes go straight into `internal/migrations/schema.sql`. No numbered migration file.
- Go style: `any` not `interface{}`, guard clauses, wrap leaf errors as `fmt.Errorf("err <action>: %w", err)`, optimistic assignment, comments only for gotchas.
- One commit per purpose. End each commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.
- Do not reset the dev database. Verify SQL on the throwaway database `ingest_check` built from `schema.sql`, then drop it. Never print `.env` values.
- Temporary `check_test.go` files run against the throwaway database and are deleted before the commit. They are never committed.
- `.svelte` edits go through the Svelte MCP `svelte-autofixer` until it reports no issues. Never use npx, use pnpm.

## Plan rulings against the spec

These fill gaps the spec leaves open. Task 8 writes them back into the kb.

- **A daily cap of 330 searches is enforced**, counted over a rolling 24 hours. The spec names a $50 monthly budget alert. $50 at $5 per 1k is 10k queries a month, 330 a day. Searching is paid, so a backlog must not spend a month's budget in a day.
- **`discovery_events.result_count` records paid calls.** It holds the number of Brave results and stays NULL when an item is marked searched without a call (dedupe or a rejected query). Brave usage per day is counted from it, not from `searched_at` alone, which dedupe also sets.
- **Newest items first.** When the cap holds back a backlog, older items age out of the window instead of crowding out fresh events.
- **`search_lang` is sent only for `sv` and `en`.** Other post languages search without it.
- **No query truncation.** The discoverer already drops queries over 100 characters, under Brave's 400.
- **Brave errors:** 429, 5xx and network errors stop the tick and leave the item untouched. 400 and 422 mark the item searched without `result_count`, so one bad query cannot block the queue. Any other status, like 401, stops the tick.
- **The tick follows the LLM schedule**, so Brave is not paid while DeepSeek is in peak hours and the filter cannot run. If the filter is still unavailable after a search, the tick stops and the item is searched again later. Other filter errors keep nothing and mark the item searched.
- **An empty result set skips the LLM** and marks the item searched with `result_count = 0`.
- **Result URLs pass the link extraction filter**: `utm_*` and fragments stripped, non-http dropped, the post's own host and blocklisted hosts skipped. URLs already in `posts` are skipped. There is no 10-link cap.
- **The filter does not see result URLs**, only the fields the spec lists. It answers with result ids.
- **Actions become tag keys** with `robot.TagKey` (lowercase, spaces and hyphens to underscores) inside the discoverer. Alias remapping happens in the discovery worker, which reads `tag_aliases` once per tick.
- **No `BRAVE_API_KEY` disables the search worker** with a warning, like the mocked classifier disables ingest.

## Review Focus

- Many posts about one event inside the window, with different casing, must cost one Brave call. Pinned in Task 4 (`Searched` with `VOLVO`) and Task 6 (two Volvo items in one tick).
- A backlog must never push searches past 330 in 24 hours. Pinned in Task 6 (budget rows, then a searcher that fails the test if called).
- Brave 429 or an unavailable LLM must not mark an item searched or lose it. Pinned in Task 3 (status mapping) and Task 6 (Saab item stays pending).
- Result URLs on blocklisted hosts, the post's own host, with tracking parameters or already in `posts` must not enter `link_events` as is. Pinned in Task 5 and Task 6 (`own.example`, `youtube.com`, `utm_source`).
- Items from proposed sources, with non-gate actions or older than 7 days must never be searched. Pinned in Task 4 (`ListQueue` check).

---

### Task 1: Gate keys and normalized actions in event extraction

**Files:**

- Create: `internal/service/robot/prompts/discovery_user.md`, `internal/service/discovery/discovery_test.go`
- Modify: `internal/service/robot/prompts/discovery.md`, `internal/service/robot/discovery.go`, `internal/service/robot/discovery_test.go`, `internal/service/discovery/discovery.go`, `internal/service/discovery/store.go`

**Interfaces:**

- Produces: `(*robot.Discoverer).Keywords(ctx context.Context, markdown string, gateKeys []string) ([]robot.Keyword, error)` with `Action` as a tag key. `(*discovery.Store).Actions(ctx) (gateKeys []string, aliases map[string]string, err error)`. `eventItems(postID string, keywords []robot.Keyword, aliases map[string]string) []DiscoverItem`.

- [ ] **Step 1: Update the robot tests**

In `internal/service/robot/discovery_test.go`, add `"strings"` to the imports. In `TestKeywords` change the Northvolt keyword to:

```go
		{Entity: "Northvolt", Action: "New  Factory", Query: "Northvolt expansion"},
```

and its expected entry to:

```go
		{Entity: "Northvolt", Action: "new_factory", Query: "Northvolt expansion"},
```

Change the call to:

```go
	keywords, err := NewDiscoverer(llm).Keywords(context.Background(), article, []string{"acquisition", "contract"})
```

and replace the last check with:

```go
	if !strings.Contains(llm.req.Prompt, article) || !strings.Contains(llm.req.Prompt, "Gate tag keys: acquisition, contract") || llm.req.System == "" {
		t.Errorf("prompt %q", llm.req.Prompt)
	}
```

Replace `TestKeywordsNone` with:

```go
func TestKeywordsNone(t *testing.T) {
	llm := &fakeLLM{}

	keywords, err := NewDiscoverer(llm).Keywords(context.Background(), article, nil)
	if err != nil || len(keywords) != 0 {
		t.Fatalf("got %v, %v", keywords, err)
	}

	if !strings.Contains(llm.req.Prompt, "Gate tag keys: none") {
		t.Errorf("prompt %q", llm.req.Prompt)
	}
}
```

- [ ] **Step 2: Write the alias test**

Create `internal/service/discovery/discovery_test.go`:

```go
package discovery

import (
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot"
)

func TestEventItems(t *testing.T) {
	got := eventItems("p", []robot.Keyword{
		{Entity: "Volvo", Action: "takeover", Query: "Volvo köper Northvolt"},
		{Entity: "Saab", Action: "order", Query: "Saab order"},
	}, map[string]string{"takeover": "acquisition"})

	want := []DiscoverItem{
		{PostID: "p", Entity: "Volvo", Action: "acquisition", Query: "Volvo köper Northvolt"},
		{PostID: "p", Entity: "Saab", Action: "order", Query: "Saab order"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("got %+v", got)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/service/robot/ ./internal/service/discovery/`
Expected: FAIL to compile, `too many arguments in call to NewDiscoverer(llm).Keywords` and `undefined: eventItems`.

- [ ] **Step 4: Prompts**

Create `internal/service/robot/prompts/discovery_user.md`:

```
Gate tag keys: {{if .Keys}}{{join .Keys ", "}}{{else}}none{{end}}

Article:
{{.Article}}
```

In `internal/service/robot/prompts/discovery.md` change the second paragraph's first sentence to `You get the gate tag keys and one article in markdown.` and replace the action line with:

```
- action: the kind of event. When one of the gate tag keys fits, use exactly that key. Otherwise one or two lowercase English words joined by an underscore, like investment or new_factory.
```

- [ ] **Step 5: Discoverer**

In `internal/service/robot/discovery.go` add `"text/template"` to the imports and after `var discoveryPrompt string`:

```go
//go:embed prompts/discovery_user.md
var discoveryUserPrompt string

var discoveryUser = template.Must(template.New("discovery_user.md").
	Funcs(template.FuncMap{"join": strings.Join}).
	Parse(discoveryUserPrompt))
```

Replace `Keywords` with:

```go
func (d *Discoverer) Keywords(ctx context.Context, markdown string, gateKeys []string) ([]Keyword, error) {
	if r := []rune(markdown); len(r) > maxDiscoveryInput {
		markdown = string(r[:maxDiscoveryInput])
	}

	var prompt strings.Builder

	if err := discoveryUser.Execute(&prompt, map[string]any{
		"Keys":    gateKeys[:min(len(gateKeys), maxExistingTags)],
		"Article": markdown,
	}); err != nil {
		return nil, err
	}

	temp := 0.2

	var answer discoveryAnswer

	if err := d.llm.Unmarshal(ctx, adapter.Request{
		System:      strings.TrimSpace(discoveryPrompt),
		Prompt:      prompt.String(),
		Temperature: &temp,
	}, &answer); err != nil {
		return nil, err
	}

	return groundKeywords(answer.Keywords, markdown), nil
}
```

In `groundKeywords` replace `k.Action = strings.ToLower(collapse(k.Action))` with `k.Action = TagKey(k.Action)`.

- [ ] **Step 6: Gate keys and aliases from the store**

In `internal/service/discovery/store.go` add:

```go
func (s *Store) Actions(ctx context.Context) (gateKeys []string, aliases map[string]string, err error) {
	rows, err := s.db.Query(ctx, `SELECT key FROM tags WHERE enabled AND status = 'active' AND gate ORDER BY key`)
	if err != nil {
		return nil, nil, fmt.Errorf("err query gate tags: %w", err)
	}

	gateKeys, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, nil, fmt.Errorf("err scan gate tags: %w", err)
	}

	rows, err = s.db.Query(ctx, `SELECT alias, key FROM tag_aliases`)
	if err != nil {
		return nil, nil, fmt.Errorf("err query tag aliases: %w", err)
	}

	aliases = make(map[string]string)

	var alias, key string

	if _, err = pgx.ForEachRow(rows, []any{&alias, &key}, func() error {
		aliases[alias] = key
		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("err scan tag aliases: %w", err)
	}

	return gateKeys, aliases, nil
}
```

- [ ] **Step 7: Worker**

In `internal/service/discovery/discovery.go` replace `discoverPosts` with:

```go
func discoverPosts(ctx context.Context, s *Store, d *robot.Discoverer, l *slog.Logger) error {
	posts, err := iterx.Collect(s.ListUndiscoveredPosts(ctx, batchSize))
	if err != nil || len(posts) == 0 {
		return err
	}

	gateKeys, aliases, err := s.Actions(ctx)
	if err != nil {
		return err
	}

	for _, post := range posts {
		keywords, err := d.Keywords(ctx, post.Content, gateKeys)

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("discover keywords", "post", post.ID, "err", err)
		}

		if err := s.CompleteDiscovery(ctx, post.ID, eventItems(post.ID, keywords, aliases)); err != nil {
			return err
		}
	}

	return nil
}

func eventItems(postID string, keywords []robot.Keyword, aliases map[string]string) []DiscoverItem {
	items := make([]DiscoverItem, 0, len(keywords))

	for _, k := range keywords {
		action := k.Action
		if key, ok := aliases[action]; ok {
			action = key
		}

		items = append(items, DiscoverItem{PostID: postID, Entity: k.Entity, Action: action, Query: k.Query})
	}

	return items
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/service/robot/ ./internal/service/discovery/`
Expected: `ok` for both.

- [ ] **Step 9: Verify the Actions SQL**

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" ingest_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d ingest_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d ingest_check' <<'SQL'
INSERT INTO tags (key, gate) VALUES ('acquisition', true), ('contract', false);
INSERT INTO tags (key, gate, enabled) VALUES ('ipo', true, false);
INSERT INTO tags (key, gate, status) VALUES ('layoffs', true, 'dormant');
INSERT INTO tag_aliases (alias, key) VALUES ('takeover', 'acquisition');
SELECT key FROM tags WHERE enabled AND status = 'active' AND gate ORDER BY key;
SELECT alias, key FROM tag_aliases;
SQL
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" ingest_check'
```

Expected: one gate key `acquisition`, one alias `takeover | acquisition`.

- [ ] **Step 10: Commit**

```bash
gofmt -l internal
go build ./... && go vet ./...
git add internal/service/robot/prompts/discovery_user.md internal/service/robot/prompts/discovery.md internal/service/robot/discovery.go internal/service/robot/discovery_test.go internal/service/discovery/discovery.go internal/service/discovery/discovery_test.go internal/service/discovery/store.go
git commit -m "Steer event actions to gate tag keys

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Search result filter

**Files:**

- Create: `internal/service/robot/search_filter.go`, `internal/service/robot/search_filter_test.go`, `internal/service/robot/prompts/search_filter.md`
- Modify: `internal/service/robot/discovery_test.go` (`fakeLLM.answer` becomes `any`)

**Interfaces:**

- Produces: `robot.SearchResult{URL, Title, Description string; ExtraSnippets []string; PageAge, Hostname string}`, `robot.NewResultFilter(llm adapter.LLM) *robot.ResultFilter`, `(*robot.ResultFilter).Keep(ctx context.Context, entity, action string, results []robot.SearchResult) ([]string, error)` returning kept URLs in answer order.

- [ ] **Step 1: Generalize the fake LLM**

In `internal/service/robot/discovery_test.go` change `answer discoveryAnswer` in `fakeLLM` to `answer any`.

- [ ] **Step 2: Write the failing test**

Create `internal/service/robot/search_filter_test.go`:

```go
package robot

import (
	"context"
	"slices"
	"strings"
	"testing"
)

var searchResults = []SearchResult{
	{URL: "https://a.example/1", Title: "Volvo köper Northvolt", Hostname: "a.example"},
	{URL: "https://b.example/2", Title: "Volvo-aktien", Hostname: "b.example"},
	{URL: "https://c.example/3", Title: "Volvo förvärvar Northvolt", Description: "Affären", ExtraSnippets: []string{"Mer"}, PageAge: "2026-10-16T08:00:00", Hostname: "c.example"},
}

func TestKeep(t *testing.T) {
	llm := &fakeLLM{answer: filterAnswer{Keep: []int{2, 0, 2, 7, -1}}}

	got, err := NewResultFilter(llm).Keep(context.Background(), "Volvo", "acquisition", searchResults)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"https://c.example/3", "https://a.example/1"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}

	for _, s := range []string{`"entity":"Volvo"`, `"action":"acquisition"`, `"id":2`, `"extra_snippets":["Mer"]`, `"page_age":"2026-10-16T08:00:00"`, `"hostname":"c.example"`} {
		if !strings.Contains(llm.req.Prompt, s) {
			t.Errorf("prompt misses %s: %s", s, llm.req.Prompt)
		}
	}

	if strings.Contains(llm.req.Prompt, "https://") || llm.req.System == "" {
		t.Errorf("prompt %s", llm.req.Prompt)
	}
}

func TestKeepNoResults(t *testing.T) {
	llm := &fakeLLM{}

	got, err := NewResultFilter(llm).Keep(context.Background(), "Volvo", "acquisition", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}

	if llm.req.System != "" {
		t.Error("model called without results")
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/service/robot/ -run Keep`
Expected: FAIL to compile, `undefined: SearchResult`.

- [ ] **Step 4: Prompt**

Create `internal/service/robot/prompts/search_filter.md`:

```
You filter web search results for a business news service.

You get one event, as the company or organisation that acts and the kind of action, and search results in JSON. Each result has an id, title, description, extra snippets, page age and hostname.

Keep a result only when it reports this same event by this same company or organisation. News articles, press releases, company pages and filings all count, from any site, small or large.

Drop a result when:
- it is about another event, even by the same company,
- it only mentions the company or the event in passing,
- it is a listing, search, tag or category page, a stock quote or a company profile,
- the text does not show that it is about this event.

Answer with the ids of the results to keep. An empty list is fine.
```

- [ ] **Step 5: Filter**

Create `internal/service/robot/search_filter.go`:

```go
package robot

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

//go:embed prompts/search_filter.md
var searchFilterPrompt string

type SearchResult struct {
	URL           string   `json:"-"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	ExtraSnippets []string `json:"extra_snippets,omitempty"`
	PageAge       string   `json:"page_age,omitempty"`
	Hostname      string   `json:"hostname"`
}

type filterResult struct {
	ID int `json:"id"`
	SearchResult
}

type filterPrompt struct {
	Entity  string         `json:"entity"`
	Action  string         `json:"action"`
	Results []filterResult `json:"results"`
}

type filterAnswer struct {
	Keep []int `json:"keep"`
}

type ResultFilter struct {
	llm adapter.LLM
}

func NewResultFilter(llm adapter.LLM) *ResultFilter {
	return &ResultFilter{llm: llm}
}

func (f *ResultFilter) Keep(ctx context.Context, entity, action string, results []SearchResult) ([]string, error) {
	if len(results) == 0 {
		return nil, nil
	}

	p := filterPrompt{Entity: entity, Action: action, Results: make([]filterResult, len(results))}

	for i, r := range results {
		p.Results[i] = filterResult{ID: i, SearchResult: r}
	}

	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("err marshal search results: %w", err)
	}

	temp := 0.0

	var answer filterAnswer

	if err := f.llm.Unmarshal(ctx, adapter.Request{
		System:      strings.TrimSpace(searchFilterPrompt),
		Prompt:      string(b),
		Temperature: &temp,
	}, &answer); err != nil {
		return nil, err
	}

	var urls []string

	for _, id := range answer.Keep {
		if id < 0 || id >= len(results) || slices.Contains(urls, results[id].URL) {
			continue
		}

		urls = append(urls, results[id].URL)
	}

	return urls, nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/service/robot/`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
gofmt -l internal
git add internal/service/robot/search_filter.go internal/service/robot/search_filter_test.go internal/service/robot/prompts/search_filter.md internal/service/robot/discovery_test.go
git commit -m "Filter search results to the searched event

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Brave searcher

**Files:**

- Create: `internal/service/discovery/brave.go`, `internal/service/discovery/brave_test.go`
- Modify: `internal/lib/config/env.go`, `.env-sample`

**Interfaces:**

- Consumes: `robot.SearchResult` from Task 2.
- Produces: `discovery.Query{Q, Lang string}`, `discovery.Searcher` interface `Search(ctx context.Context, q Query) ([]robot.SearchResult, error)`, `discovery.NewBrave(env *config.Environment) (Searcher, error)`, `errSearchRejected`. 429, 5xx and network errors wrap `adapter.ErrUnavailable`. 400 and 422 wrap `errSearchRejected`.

- [ ] **Step 1: Write the failing test**

Create `internal/service/discovery/brave_test.go`:

```go
package discovery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

func testBrave(t *testing.T, handler http.HandlerFunc) brave {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return brave{client: srv.Client(), url: srv.URL, key: "secret"}
}

func TestBraveSearch(t *testing.T) {
	b := testBrave(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("X-Subscription-Token") != "secret" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("request %s %v", r.Method, r.Header)
		}

		q := r.URL.Query()
		want := map[string]string{
			"q": "Volvo köper Northvolt", "country": "SE", "search_lang": "sv", "freshness": "pw",
			"count": "20", "result_filter": "web,news", "extra_snippets": "true",
		}

		for k, v := range want {
			if q.Get(k) != v {
				t.Errorf("%s = %q", k, q.Get(k))
			}
		}

		w.Write([]byte(`{
			"web": {"results": [
				{"url": "https://a.example/1", "title": "A", "description": "Da", "extra_snippets": ["Ea"], "page_age": "2026-10-16T08:00:00", "meta_url": {"hostname": "a.example"}},
				{"url": "https://b.example/2", "title": "B", "meta_url": {"hostname": "b.example"}}
			]},
			"news": {"results": [
				{"url": "https://b.example/2", "title": "B again"},
				{"url": "https://c.example/3", "title": "C", "meta_url": {"hostname": "c.example"}}
			]}
		}`))
	})

	got, err := b.Search(context.Background(), Query{Q: "Volvo köper Northvolt", Lang: "sv"})
	if err != nil {
		t.Fatal(err)
	}

	var urls []string

	for _, r := range got {
		urls = append(urls, r.URL)
	}

	if !slices.Equal(urls, []string{"https://a.example/1", "https://b.example/2", "https://c.example/3"}) {
		t.Fatalf("urls %v", urls)
	}

	a := got[0]
	if a.Title != "A" || a.Description != "Da" || !slices.Equal(a.ExtraSnippets, []string{"Ea"}) || a.PageAge != "2026-10-16T08:00:00" || a.Hostname != "a.example" {
		t.Errorf("result %+v", a)
	}

	if got[1].Title != "B" {
		t.Errorf("web result not kept over news duplicate: %+v", got[1])
	}
}

func TestBraveOtherLanguage(t *testing.T) {
	b := testBrave(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("search_lang") {
			t.Errorf("search_lang %q", r.URL.Query().Get("search_lang"))
		}

		w.Write([]byte(`{}`))
	})

	if got, err := b.Search(context.Background(), Query{Q: "x", Lang: "de"}); err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestBraveErrors(t *testing.T) {
	cases := map[int]error{
		http.StatusTooManyRequests:     adapter.ErrUnavailable,
		http.StatusServiceUnavailable:  adapter.ErrUnavailable,
		http.StatusUnprocessableEntity: errSearchRejected,
		http.StatusBadRequest:          errSearchRejected,
		http.StatusUnauthorized:        nil,
	}

	for status, want := range cases {
		b := testBrave(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		})

		_, err := b.Search(context.Background(), Query{Q: "x"})

		switch {
		case err == nil:
			t.Errorf("%d: no error", status)
		case want != nil && !errors.Is(err, want):
			t.Errorf("%d: got %v", status, err)
		case want == nil && (errors.Is(err, adapter.ErrUnavailable) || errors.Is(err, errSearchRejected)):
			t.Errorf("%d: got %v", status, err)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/service/discovery/ -run Brave`
Expected: FAIL to compile, `undefined: brave`.

- [ ] **Step 3: Config**

In `internal/lib/config/env.go` add after `TypesafeAPIKey`:

```go
	BraveAPIKey      string `env:"BRAVE_API_KEY" envDefault:""`
```

In `.env-sample` add `BRAVE_API_KEY=` on its own line after the last API key line.

- [ ] **Step 4: Searcher**

Create `internal/service/discovery/brave.go`:

```go
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/config"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	braveURL     = "https://api.search.brave.com/res/v1/web/search"
	braveTimeout = 20 * time.Second
	braveCount   = 20
)

var errSearchRejected = errors.New("discovery: search rejected")

type Query struct {
	Q    string
	Lang string
}

type Searcher interface {
	Search(ctx context.Context, q Query) ([]robot.SearchResult, error)
}

type brave struct {
	client *http.Client
	url    string
	key    string
}

func NewBrave(env *config.Environment) (Searcher, error) {
	if env.BraveAPIKey == "" {
		return nil, errors.New("brave: no api key")
	}

	return brave{client: &http.Client{Timeout: braveTimeout}, url: braveURL, key: env.BraveAPIKey}, nil
}

type braveResult struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	ExtraSnippets []string `json:"extra_snippets"`
	PageAge       string   `json:"page_age"`
	MetaURL       struct {
		Hostname string `json:"hostname"`
	} `json:"meta_url"`
}

type braveResponse struct {
	Web struct {
		Results []braveResult `json:"results"`
	} `json:"web"`
	News struct {
		Results []braveResult `json:"results"`
	} `json:"news"`
}

func (b brave) Search(ctx context.Context, q Query) ([]robot.SearchResult, error) {
	params := url.Values{
		"q":              {q.Q},
		"country":        {"SE"},
		"freshness":      {"pw"},
		"count":          {strconv.Itoa(braveCount)},
		"result_filter":  {"web,news"},
		"extra_snippets": {"true"},
	}

	if q.Lang == "sv" || q.Lang == "en" {
		params.Set("search_lang", q.Lang)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.url+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("err build brave request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.key)

	res, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: brave: %w", adapter.ErrUnavailable, err)
	}
	defer res.Body.Close()

	switch {
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= http.StatusInternalServerError:
		return nil, fmt.Errorf("%w: brave %s", adapter.ErrUnavailable, res.Status)
	case res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnprocessableEntity:
		return nil, fmt.Errorf("%w: brave %s", errSearchRejected, res.Status)
	case res.StatusCode != http.StatusOK:
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("brave: %s: %s", res.Status, bytes.TrimSpace(msg))
	}

	var body braveResponse

	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("err decode brave response: %w", err)
	}

	var out []robot.SearchResult

	seen := make(map[string]bool)

	for _, r := range slices.Concat(body.Web.Results, body.News.Results) {
		if r.URL == "" || seen[r.URL] {
			continue
		}

		seen[r.URL] = true
		out = append(out, robot.SearchResult{
			URL:           r.URL,
			Title:         r.Title,
			Description:   r.Description,
			ExtraSnippets: r.ExtraSnippets,
			PageAge:       r.PageAge,
			Hostname:      r.MetaURL.Hostname,
		})
	}

	return out, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/service/discovery/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal
git add internal/service/discovery/brave.go internal/service/discovery/brave_test.go internal/lib/config/env.go .env-sample
git commit -m "Add Brave web and news searcher

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Search switch, schema and queue store

**Files:**

- Modify: `internal/migrations/schema.sql`, `internal/service/switches/store.go`, `internal/service/discovery/model.go`, `internal/service/discovery/store.go`
- Temporary: `internal/service/discovery/check_test.go` (deleted before commit)

**Interfaces:**

- Produces: `discovery_events.result_count INT`, switch row `search_discovery`, `switches.SearchDiscovery`. `discovery.SearchItem{ID, PostID, Entity, Action, Query, Lang, BaseURL string}`. On `*discovery.Store`: `ListQueue(ctx, windowDays, limit int) ([]SearchItem, error)` newest first, `Searched(ctx, entity, action string, windowDays int) (bool, error)`, `SearchesLastDay(ctx) (int, error)`, `QueueResults(ctx, item SearchItem, urls []string, resultCount int) (int64, error)`, existing `MarkSearched(ctx, id string) error`.

- [ ] **Step 1: Schema and switch**

In `internal/migrations/schema.sql`, in `discovery_events` after `searched_at TIMESTAMPTZ,` add:

```sql
  result_count INT, -- Brave results, NULL when marked searched without a call
```

Change the switch insert to:

```sql
INSERT INTO switches (key) VALUES ('shadow_gate'), ('discovered_ingest'), ('search_discovery') ON CONFLICT (key) DO NOTHING;
```

In `internal/service/switches/store.go` add `SearchDiscovery = "search_discovery"` to the const block.

- [ ] **Step 2: Write the temporary check**

Create `internal/service/discovery/check_test.go`:

```go
package discovery

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/database"
)

func checkDB(t *testing.T) *database.Database {
	t.Helper()

	conn := os.Getenv("INGEST_CHECK_CONN")
	if conn == "" {
		t.Skip("INGEST_CHECK_CONN not set")
	}

	db, err := database.NewDatabase(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(db.Close)

	return db
}

func TestQueueCheck(t *testing.T) {
	ctx := context.Background()
	db := checkDB(t)

	if _, err := db.Exec(ctx, `
		INSERT INTO tags (key, gate) VALUES ('acquisition', true), ('contract', false);
		INSERT INTO domains (id, base_url) VALUES
			('00000000-0000-0000-0000-000000000001', 'https://own.example'),
			('00000000-0000-0000-0000-000000000004', 'https://other.example');
		INSERT INTO sources (id, domain_id, adapter, url) VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'rss', 'https://own.example/rss');
		INSERT INTO sources (id, domain_id, adapter, url, origin, status) VALUES ('00000000-0000-0000-0000-000000000005', '00000000-0000-0000-0000-000000000004', 'page', 'https://other.example', 'discovered', 'proposed');
		INSERT INTO posts (id, source_id, domain_id, url, lang, title, short_content, content, content_hash) VALUES
			('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'https://own.example/p1', 'sv', 'p1', '', 'c', '\x00'),
			('00000000-0000-0000-0000-0000000000a2', '00000000-0000-0000-0000-000000000005', '00000000-0000-0000-0000-000000000004', 'https://other.example/p2', 'sv', 'p2', '', 'c', '\x00'),
			('00000000-0000-0000-0000-0000000000a3', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'https://existing.example/p', 'sv', 'p3', '', 'c', '\x00');
		INSERT INTO discovery_events (id, post_id, entity, action, query, searched_at, result_count, created_at) VALUES
			('00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000a1', 'Saab', 'acquisition', 'Saab köper', NULL, NULL, now() - interval '1 hour'),
			('00000000-0000-0000-0000-0000000000e2', '00000000-0000-0000-0000-0000000000a2', 'Saab', 'acquisition', 'Saab förvärv', NULL, NULL, now()),
			('00000000-0000-0000-0000-0000000000e3', '00000000-0000-0000-0000-0000000000a1', 'Saab', 'contract', 'Saab avtal', NULL, NULL, now()),
			('00000000-0000-0000-0000-0000000000e4', '00000000-0000-0000-0000-0000000000a1', 'Saab', 'acquisition', 'Saab gammal', NULL, NULL, now() - interval '8 days'),
			('00000000-0000-0000-0000-0000000000e5', '00000000-0000-0000-0000-0000000000a1', 'Volvo', 'acquisition', 'Volvo köper', now(), 12, now()),
			('00000000-0000-0000-0000-0000000000e6', '00000000-0000-0000-0000-0000000000a1', 'Ericsson', 'acquisition', 'Ericsson köper', NULL, NULL, now()),
			('00000000-0000-0000-0000-0000000000e7', '00000000-0000-0000-0000-0000000000a1', 'Volvo', 'acquisition', 'Volvo förvärv', now(), NULL, now());
		INSERT INTO link_events (url, via, from_post_id) VALUES ('https://queued.example/q', 'link', '00000000-0000-0000-0000-0000000000a1');
		`); err != nil {
		t.Fatal(err)
	}

	s := NewStore(db)

	items, err := s.ListQueue(ctx, 7, 10)
	if err != nil {
		t.Fatal(err)
	}

	var ids []string

	for _, it := range items {
		ids = append(ids, it.ID)
	}

	if want := []string{"00000000-0000-0000-0000-0000000000e6", "00000000-0000-0000-0000-0000000000e1"}; !slices.Equal(ids, want) {
		t.Fatalf("listed %v", ids)
	}

	if it := items[1]; it.PostID != "00000000-0000-0000-0000-0000000000a1" || it.Lang != "sv" || it.BaseURL != "https://own.example" || it.Query != "Saab köper" || it.Entity != "Saab" || it.Action != "acquisition" {
		t.Errorf("item %+v", it)
	}

	if done, err := s.Searched(ctx, "VOLVO", "Acquisition", 7); err != nil || !done {
		t.Errorf("volvo searched: %v %v", done, err)
	}

	if done, err := s.Searched(ctx, "Saab", "acquisition", 7); err != nil || done {
		t.Errorf("saab searched: %v %v", done, err)
	}

	if n, err := s.SearchesLastDay(ctx); err != nil || n != 1 {
		t.Errorf("searches %d %v", n, err)
	}

	queued, err := s.QueueResults(ctx, items[1], []string{"https://news.example/a", "https://existing.example/p", "https://queued.example/q"}, 17)
	if err != nil || queued != 1 {
		t.Fatalf("queued %d %v", queued, err)
	}

	var via, from, item string

	if err := db.QueryRow(ctx, `SELECT via, from_post_id::text, discovery_events_id::text FROM link_events WHERE url = 'https://news.example/a'`).Scan(&via, &from, &item); err != nil {
		t.Fatal(err)
	}

	if via != "search" || from != "00000000-0000-0000-0000-0000000000a1" || item != "00000000-0000-0000-0000-0000000000e1" {
		t.Errorf("link %s %s %s", via, from, item)
	}

	var count *int

	if err := db.QueryRow(ctx, `SELECT result_count FROM discovery_events WHERE id = '00000000-0000-0000-0000-0000000000e1' AND searched_at IS NOT NULL`).Scan(&count); err != nil || count == nil || *count != 17 {
		t.Errorf("result count %v %v", count, err)
	}

	if err := s.MarkSearched(ctx, "00000000-0000-0000-0000-0000000000e6"); err != nil {
		t.Fatal(err)
	}

	if n, err := s.SearchesLastDay(ctx); err != nil || n != 2 {
		t.Errorf("searches after mark %d %v", n, err)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go vet ./internal/service/discovery/`
Expected: FAIL, `undefined: SearchItem` or wrong argument count to `s.ListQueue`.

- [ ] **Step 4: Model**

In `internal/service/discovery/model.go` add:

```go
type SearchItem struct {
	ID      string
	PostID  string
	Entity  string
	Action  string
	Query   string
	Lang    string
	BaseURL string
}
```

- [ ] **Step 5: Store**

In `internal/service/discovery/store.go` delete `queueColumns`, the old `ListQueue` and `scanQueueItem`, and add:

```go
func (s *Store) ListQueue(ctx context.Context, windowDays, limit int) ([]SearchItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT q.id, q.post_id, q.entity, q.action, q.query, p.lang, d.base_url
		FROM discovery_events q
		JOIN posts p ON p.id = q.post_id
		JOIN sources s ON s.id = p.source_id
		JOIN domains d ON d.id = p.domain_id
		JOIN tags t ON t.key = q.action AND t.enabled AND t.status = 'active' AND t.gate
		WHERE q.searched_at IS NULL
			AND s.status IN ('active', 'dormant')
			AND q.created_at > now() - make_interval(days => $1)
		ORDER BY q.created_at DESC
		LIMIT $2
		`, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("err query search queue: %w", err)
	}

	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (i SearchItem, err error) {
		err = row.Scan(&i.ID, &i.PostID, &i.Entity, &i.Action, &i.Query, &i.Lang, &i.BaseURL)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan search queue: %w", err)
	}

	return items, nil
}

func (s *Store) Searched(ctx context.Context, entity, action string, windowDays int) (found bool, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM discovery_events
			WHERE lower(entity) = lower($1)
				AND lower(action) = lower($2)
				AND searched_at > now() - make_interval(days => $3)
		)
		`, entity, action, windowDays).Scan(&found); err != nil {
		return false, fmt.Errorf("err check searched event: %w", err)
	}

	return found, nil
}

func (s *Store) SearchesLastDay(ctx context.Context) (n int, err error) {
	if err = s.db.QueryRow(ctx, `
		SELECT count(*) FROM discovery_events
		WHERE searched_at > now() - interval '1 day' AND result_count IS NOT NULL
		`).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count searches: %w", err)
	}

	return n, nil
}

func (s *Store) QueueResults(ctx context.Context, item SearchItem, urls []string, resultCount int) (queued int64, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO link_events (url, via, from_post_id, discovery_events_id)
			SELECT u, 'search', $2, $3 FROM unnest($1::text[]) AS u
			WHERE NOT EXISTS (SELECT 1 FROM posts p WHERE p.url = u)
			ON CONFLICT (url) DO NOTHING
			`, urls, item.PostID, item.ID)
		if err != nil {
			return fmt.Errorf("err queue search results: %w", err)
		}

		queued = tag.RowsAffected()

		if _, err := tx.Exec(ctx, `UPDATE discovery_events SET searched_at = now(), result_count = $2 WHERE id = $1`, item.ID, resultCount); err != nil {
			return fmt.Errorf("err mark searched: %w", err)
		}

		return nil
	})

	return
}
```

The final `SearchesLastDay` of 2 counts `e5` and `e1`. `MarkSearched` on `e6` leaves `result_count` NULL, so it adds nothing.

- [ ] **Step 6: Run the check**

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" ingest_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d ingest_check' < internal/migrations/schema.sql
(set -a; . ./.env; INGEST_CHECK_CONN="postgresql://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:5432/ingest_check?sslmode=disable" go test ./internal/service/discovery/ -run Check -count=1 > "$SCRATCHPAD/discovery_check.log" 2>&1; echo exit $?)
tail -40 "$SCRATCHPAD/discovery_check.log"
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d ingest_check -c "SELECT key, enabled FROM switches ORDER BY key"'
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" ingest_check'
```

Expected: `exit 0`, `ok`, and three switch rows all `f`.

- [ ] **Step 7: Delete the check and commit**

```bash
rm internal/service/discovery/check_test.go
gofmt -l internal
go build ./... && go vet ./... && go test ./internal/service/discovery/
git add internal/migrations/schema.sql internal/service/switches/store.go internal/service/discovery/model.go internal/service/discovery/store.go
git commit -m "Store searchable events and queue search results

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Shared link filter

**Files:**

- Modify: `internal/service/ingest/links.go`, `internal/service/ingest/links_test.go`

**Interfaces:**

- Produces: `ingest.FilterLinks(raw []string, ownBaseURL string) []string`: normalized, deduplicated, without the own host, blocklisted hosts and non-http links, in input order, uncapped. `outboundLinks` keeps its behaviour.

- [ ] **Step 1: Write the failing test**

Append to `internal/service/ingest/links_test.go`:

```go
func TestFilterLinks(t *testing.T) {
	got := FilterLinks([]string{
		"https://news.example/a?utm_source=x#top",
		"https://www.own.example/x",
		"https://www.youtube.com/watch?v=1",
		"ftp://files.example/f",
		"https://news.example/a",
		"https://other.example/b",
	}, "https://own.example")

	if want := []string{"https://news.example/a", "https://other.example/b"}; !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/service/ingest/ -run Links`
Expected: FAIL to compile, `undefined: FilterLinks`.

- [ ] **Step 3: Split the filter out**

In `internal/service/ingest/links.go` replace `outboundLinks` with:

```go
func outboundLinks(markdown, ownBaseURL string) []string {
	var raw []string

	for _, m := range markdownLink.FindAllStringSubmatch(markdown, -1) {
		if m[1] == "!" {
			continue
		}

		raw = append(raw, m[2])
	}

	links := FilterLinks(raw, ownBaseURL)

	return links[:min(len(links), maxLinksPerPost)]
}

func FilterLinks(raw []string, ownBaseURL string) []string {
	own := hostOf(ownBaseURL)

	var out []string

	for _, r := range raw {
		link, ok := normalizeLink(r)
		if !ok || slices.Contains(out, link) {
			continue
		}

		if host := hostOf(link); host == own || blocked(host) {
			continue
		}

		out = append(out, link)
	}

	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/service/ingest/`
Expected: `ok`, including the existing `TestOutboundLinks*`.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal
git add internal/service/ingest/links.go internal/service/ingest/links_test.go
git commit -m "Share the outbound link filter

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Search worker

**Files:**

- Create: `internal/service/discovery/search.go`
- Modify: `cmd/main.go`
- Temporary: `internal/service/discovery/check_test.go` (deleted before commit)

**Interfaces:**

- Consumes: `Searcher`, `Query`, `errSearchRejected` (Task 3), `Store.ListQueue`, `Searched`, `SearchesLastDay`, `QueueResults`, `MarkSearched` (Task 4), `ingest.FilterLinks` (Task 5), `robot.ResultFilter` (Task 2), `switches.SearchDiscovery`.
- Produces: `discovery.RunSearch(ctx context.Context, s *Store, sw *switches.Store, se Searcher, f *robot.ResultFilter, sched schedule.Schedule, l *slog.Logger) error`, `searchEvents(...)`, `dailySearchBudget = 330`.

- [ ] **Step 1: Write the temporary check**

Create `internal/service/discovery/check_test.go` with `checkDB` exactly as in Task 4 Step 2 and:

```go
type fakeSearcher struct {
	t       *testing.T
	queries []Query
	results map[string][]robot.SearchResult
	fail    map[string]error
	forbid  bool
}

func (f *fakeSearcher) Search(_ context.Context, q Query) ([]robot.SearchResult, error) {
	if f.forbid {
		f.t.Errorf("searched %q", q.Q)
	}

	f.queries = append(f.queries, q)

	return f.results[q.Q], f.fail[q.Q]
}

type keepAll struct{}

func (keepAll) Schedule() schedule.Schedule { return schedule.Always }

func (keepAll) Prompt(context.Context, adapter.Request) (string, error) { return "", nil }

func (keepAll) Unmarshal(_ context.Context, _ adapter.Request, out any) error {
	return json.Unmarshal([]byte(`{"keep":[0,1,2]}`), out)
}

func TestSearchCheck(t *testing.T) {
	ctx := context.Background()
	db := checkDB(t)
	l := slog.New(slog.NewTextHandler(io.Discard, nil))

	if _, err := db.Exec(ctx, `
		INSERT INTO tags (key, gate) VALUES ('acquisition', true);
		INSERT INTO domains (id, base_url) VALUES ('00000000-0000-0000-0000-000000000001', 'https://own.example');
		INSERT INTO sources (id, domain_id, adapter, url) VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'rss', 'https://own.example/rss');
		INSERT INTO posts (id, source_id, domain_id, url, lang, title, short_content, content, content_hash) VALUES
			('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'https://own.example/p1', 'sv', 'p1', '', 'c', '\x00');
		INSERT INTO discovery_events (id, post_id, entity, action, query, created_at) VALUES
			('00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000a1', 'Volvo', 'acquisition', 'Volvo köper Northvolt', now() - interval '2 hours'),
			('00000000-0000-0000-0000-0000000000e2', '00000000-0000-0000-0000-0000000000a1', 'volvo', 'acquisition', 'Volvo förvärv Northvolt', now() - interval '1 hour'),
			('00000000-0000-0000-0000-0000000000e3', '00000000-0000-0000-0000-0000000000a1', 'Saab', 'acquisition', 'Saab köper', now() - interval '3 hours');
		`); err != nil {
		t.Fatal(err)
	}

	s := NewStore(db)
	sw := switches.NewStore(db)
	f := robot.NewResultFilter(keepAll{})

	off := &fakeSearcher{t: t, forbid: true}
	if err := searchEvents(ctx, &s, &sw, off, f, l); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(ctx, `UPDATE switches SET enabled = true WHERE key = 'search_discovery'`); err != nil {
		t.Fatal(err)
	}

	se := &fakeSearcher{
		t: t,
		results: map[string][]robot.SearchResult{"Volvo förvärv Northvolt": {
			{URL: "https://news.example/a?utm_source=x", Title: "a"},
			{URL: "https://www.own.example/x", Title: "own"},
			{URL: "https://www.youtube.com/watch?v=1", Title: "video"},
			{URL: "https://other.example/b", Title: "dropped"},
		}},
		fail: map[string]error{"Saab köper": fmt.Errorf("%w: brave 429", adapter.ErrUnavailable)},
	}

	if err := searchEvents(ctx, &s, &sw, se, f, l); !errors.Is(err, adapter.ErrUnavailable) {
		t.Fatalf("tick error %v", err)
	}

	var queries []string

	for _, q := range se.queries {
		queries = append(queries, q.Q)

		if q.Lang != "sv" {
			t.Errorf("lang %q", q.Lang)
		}
	}

	if want := []string{"Volvo förvärv Northvolt", "Saab köper"}; !slices.Equal(queries, want) {
		t.Errorf("queries %v", queries)
	}

	rows, err := db.Query(ctx, `SELECT url FROM link_events WHERE via = 'search' AND discovery_events_id = '00000000-0000-0000-0000-0000000000e2' ORDER BY url`)
	if err != nil {
		t.Fatal(err)
	}

	urls, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"https://news.example/a"}; !slices.Equal(urls, want) {
		t.Errorf("queued %v", urls)
	}

	rows, err = db.Query(ctx, `SELECT id::text || ':' || coalesce(result_count::text, '-') || ':' || (searched_at IS NOT NULL)::text FROM discovery_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}

	states, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"00000000-0000-0000-0000-0000000000e1:-:true",
		"00000000-0000-0000-0000-0000000000e2:4:true",
		"00000000-0000-0000-0000-0000000000e3:-:false",
	}

	if !slices.Equal(states, want) {
		t.Errorf("states %v", states)
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO discovery_events (post_id, entity, action, query, searched_at, result_count)
		SELECT '00000000-0000-0000-0000-0000000000a1', 'Budget', 'acquisition', 'budget ' || i, now(), 1
		FROM generate_series(1, 329) AS i
		`); err != nil {
		t.Fatal(err)
	}

	capped := &fakeSearcher{t: t, forbid: true}
	if err := searchEvents(ctx, &s, &sw, capped, f, l); err != nil {
		t.Fatal(err)
	}
}
```

Imports: `context`, `encoding/json`, `errors`, `fmt`, `io`, `log/slog`, `os`, `slices`, `testing`, `internal/lib/database`, `internal/lib/schedule`, `internal/service/robot`, `internal/service/robot/adapter`, `internal/service/switches`, `github.com/jackc/pgx/v5`.

The budget block adds 329 paid rows to the one from `e2`, so the last tick sees 330 and must not call the searcher, although `e3` is still pending.

- [ ] **Step 2: Run it to verify it fails**

Run: `go vet ./internal/service/discovery/`
Expected: FAIL, `undefined: searchEvents`.

- [ ] **Step 3: Worker**

Create `internal/service/discovery/search.go`:

```go
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/ingest"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/switches"
)

const (
	searchInterval = time.Minute
	searchBatch    = 5
	dedupeWindow   = 7
	// $50 a month at $5 per 1k queries, spread over 30 days.
	dailySearchBudget = 330
)

func RunSearch(ctx context.Context, s *Store, sw *switches.Store, se Searcher, f *robot.ResultFilter, sched schedule.Schedule, l *slog.Logger) error {
	for range sched.Tick(ctx, searchInterval) {
		if err := searchEvents(ctx, s, sw, se, f, l); err != nil {
			l.Error("search events", "err", err)
		}
	}

	return ctx.Err()
}

func searchEvents(ctx context.Context, s *Store, sw *switches.Store, se Searcher, f *robot.ResultFilter, l *slog.Logger) error {
	on, err := sw.Enabled(ctx, switches.SearchDiscovery)
	if err != nil || !on {
		return err
	}

	used, err := s.SearchesLastDay(ctx)
	if err != nil || used >= dailySearchBudget {
		return err
	}

	items, err := s.ListQueue(ctx, dedupeWindow, min(searchBatch, dailySearchBudget-used))
	if err != nil {
		return err
	}

	var searches, deduped, aiCalls int
	var queued int64

	defer func() {
		if len(items) > 0 {
			l.Info("search discovery", "processed", len(items), "searches", searches, "deduped", deduped, "queued", queued, "aiCalls", aiCalls)
		}
	}()

	for _, item := range items {
		done, err := s.Searched(ctx, item.Entity, item.Action, dedupeWindow)
		if err != nil {
			return err
		}

		if done {
			if err := s.MarkSearched(ctx, item.ID); err != nil {
				return fmt.Errorf("err mark deduped: %w", err)
			}

			deduped++

			continue
		}

		results, err := se.Search(ctx, Query{Q: item.Query, Lang: item.Lang})
		if errors.Is(err, errSearchRejected) {
			l.Warn("search rejected", "item", item.ID, "err", err)

			if err := s.MarkSearched(ctx, item.ID); err != nil {
				return fmt.Errorf("err mark rejected: %w", err)
			}

			continue
		}

		if err != nil {
			return fmt.Errorf("err search %s: %w", item.ID, err)
		}

		searches++

		if len(results) > 0 {
			aiCalls++
		}

		kept, err := f.Keep(ctx, item.Entity, item.Action, results)

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("filter search results", "item", item.ID, "err", err)
		}

		n, err := s.QueueResults(ctx, item, ingest.FilterLinks(kept, item.BaseURL), len(results))
		if err != nil {
			return err
		}

		queued += n
	}

	return nil
}
```

- [ ] **Step 4: Run the check**

Run the commands from Task 4 Step 6 with `discovery_check.log`.
Expected: `exit 0`, `ok`.

- [ ] **Step 5: Wire the worker**

In `cmd/main.go`, after `tagMerger := robot.NewTagMerger(classifier)`:

```go
	resultFilter := robot.NewResultFilter(llm)
	searcher, searchErr := discovery.NewBrave(env)
```

and after the discovery `g.Go` block:

```go
	g.Go(func() error {
		if searchErr != nil {
			l.Warn("search discovery off", "err", searchErr)
			return nil
		}

		return discovery.RunSearch(ctx, &discoveryStore, &switchStore, searcher, resultFilter, llm.Schedule(), l.WithGroup("search"))
	})
```

- [ ] **Step 6: Delete the check, build and test**

```bash
rm internal/service/discovery/check_test.go
gofmt -l internal cmd
go build ./... && go vet ./... && go test ./...
```

Expected: no gofmt output, every package `ok` or `no test files`.

- [ ] **Step 7: Commit**

```bash
git add internal/service/discovery/search.go cmd/main.go
git commit -m "Search Brave for gate tag events behind a switch

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Admin

**Files:**

- Modify: `frontend/src/routes/admin/admin.remote.ts`, `frontend/src/routes/admin/+page.svelte`, `frontend/src/routes/admin/switches/+page.svelte`

**Interfaces:**

- Consumes: `discovery_events.result_count`, switch `search_discovery`, `dailySearchBudget = 330`.
- Produces: `getStats().searchesToday: number`, readiness text for `search_discovery`.

- [ ] **Step 1: Baseline**

Run: `pnpm -C frontend check && pnpm -C frontend lint`
Expected: note the existing warnings (prettier on `eslint.config.js`, `tags/+page.svelte`, `tsconfig.json`). New warnings after this task must be zero.

- [ ] **Step 2: Remote functions**

In `admin.remote.ts`, in `getStats` add `searchesToday: number;` to the row type and after the `posts_today` line:

```sql
			(SELECT count(*)::int FROM discovery_events WHERE searched_at > now() - interval '24 hours' AND result_count IS NOT NULL) AS searches_today
```

(add the comma after `posts_today`). In `listSwitches` add after the `discovered_ingest` branch:

```sql
				WHEN 'search_discovery' THEN (
					SELECT coalesce(round(avg((status = 'accepted')::int) * 100) || '% accepted', 'no acceptance rate')
						|| ' of ' || count(*) || ' gate decisions in 7 days'
					FROM link_events
					WHERE status IN ('accepted', 'rejected') AND checked_at > now() - interval '7 days'
				)
```

- [ ] **Step 3: Overview**

In `admin/+page.svelte` add to the script:

```ts
// Mirrors dailySearchBudget in internal/service/discovery/search.go
const SEARCH_BUDGET = 330;
```

and before `<div class="mt-6 flex gap-4 text-sm">`:

```svelte
<h2 class="mt-8 mb-2 font-medium">Search discovery</h2>

<div class="grid grid-cols-2 gap-4 sm:grid-cols-5">
	<div class="rounded border border-zinc-200 bg-white p-4">
		<div class="text-sm text-zinc-500">Brave searches (24h)</div>
		<div class="text-2xl font-semibold tabular-nums">{stats.searchesToday} / {SEARCH_BUDGET}</div>
	</div>
</div>
```

- [ ] **Step 4: Switch description**

In `switches/+page.svelte` add to `descriptions`:

```ts
search_discovery: "Searches Brave for events from trusted posts whose action is a gate tag and queues relevant results for ingest. Event extraction runs either way. At most 330 searches a day.";
```

- [ ] **Step 5: Autofix and check**

Run the Svelte MCP `svelte-autofixer` on each changed `.svelte` file until it reports no issues. Then:

Run: `pnpm -C frontend check && pnpm -C frontend lint`
Expected: 0 errors and no warnings beyond the Step 1 baseline. Run prettier `--write` only on the changed files if it complains about them.

- [ ] **Step 6: Verify the SQL**

Build a throwaway database as in Task 4 Step 6, then:

```bash
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d ingest_check' <<'SQL'
SELECT s.key, CASE s.key
	WHEN 'search_discovery' THEN (
		SELECT coalesce(round(avg((status = 'accepted')::int) * 100) || '% accepted', 'no acceptance rate')
			|| ' of ' || count(*) || ' gate decisions in 7 days'
		FROM link_events
		WHERE status IN ('accepted', 'rejected') AND checked_at > now() - interval '7 days'
	)
END AS readiness
FROM switches s ORDER BY s.key;
SELECT (SELECT count(*)::int FROM discovery_events WHERE searched_at > now() - interval '24 hours' AND result_count IS NOT NULL) AS searches_today;
SQL
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" ingest_check'
```

Expected: `search_discovery | no acceptance rate of 0 gate decisions in 7 days` and `searches_today 0`.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/routes/admin/admin.remote.ts frontend/src/routes/admin/+page.svelte frontend/src/routes/admin/switches/+page.svelte
git commit -m "Show search usage and readiness in admin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Knowledge base

**Files:**

- Modify: `docs/kb/search-discovery.md`, `docs/kb/rollout.md`, `docs/kb/architecture.md`, `docs/kb/index.md`

- [ ] **Step 1: Search discovery**

Status `done`. Replace Current state with:

```markdown
- [discovery.go](../../internal/service/discovery/discovery.go) extracts events from every post. The prompt lists the gate tag keys, actions become tag keys and are remapped through `tag_aliases`.
- [search.go](../../internal/service/discovery/search.go) searches while `search_discovery` is on, through [brave.go](../../internal/service/discovery/brave.go), and filters results with [search_filter.go](../../internal/service/robot/search_filter.go).
- Admin shows Brave searches in the last 24 hours against the daily cap.
```

Add after step 9:

```markdown
Rules:

- At most 330 searches in a rolling 24 hours, the $50 monthly budget spread over 30 days.
- `result_count` holds the Brave result count of a paid call. It stays NULL when an item is marked searched without a call, so usage counts only paid calls.
- Newest items first. When the cap holds back a backlog, older items age out of the window.
- `search_lang` only for `sv` and `en`.
- 429, 5xx and network errors stop the tick and leave the item. 400 and 422 mark it searched without a call. Other statuses stop the tick.
- The tick follows the LLM schedule. If the filter is unavailable after a search, the item is searched again later. Other filter errors keep nothing.
- No results means no LLM call.
- Result URLs pass the link extraction filter, without its 10-link cap, and skip URLs already in `posts`.
- The filter never sees result URLs and answers with result ids.
- Without `BRAVE_API_KEY` the search worker does not start.
```

In Schema changes add:

```sql
ALTER TABLE discovery_events ADD COLUMN result_count INT;
```

In Defaults add the row `| Daily search cap | 330 | $50 a month at $5 per 1k queries over 30 days. |` and `| Searches per tick | 5 | Every minute, inside the cap. |`.

- [ ] **Step 2: Rollout**

Current state: `Phases 1 to 5 are done, with the switches table and admin page. \`shadow_gate\`, \`discovered_ingest\` and \`search_discovery\` start off.`In Monitoring change the Brave line to`Brave usage per day is counted from \`discovery_events.result_count\` over the last 24 hours of \`searched_at\`.`

- [ ] **Step 3: Architecture**

Change the Discovery row job to `Extracts events with gate tag keys as actions into \`discovery_events\` and links \`same_event\` posts.` Add a row after it:

```markdown
| Search | [search.go](../../internal/service/discovery/search.go) | Searches Brave for gate tag events from trusted posts and queues filtered results while `search_discovery` is on. |
```

Add `- Brave is the web search API.` after the Jev line. In Unused parts of the schema delete the `discovery_events` and `link_events.discovery_events_id` lines.

- [ ] **Step 4: Index**

Set the Search discovery row status to `done`.

- [ ] **Step 5: Commit**

```bash
git add docs/kb
git commit -m "Describe search discovery in kb

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
