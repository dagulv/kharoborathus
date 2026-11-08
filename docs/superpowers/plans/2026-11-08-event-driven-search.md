# Event-driven Search Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the two discovery tickers in `internal/adapter/search` with an event-driven step 3, `Discover`, and step 4, `RunSearch`, both working against the reworked `001_init.sql`.

**Architecture:** Everything stays in package `discovery` at `internal/adapter/search`. `Discover` runs inside the caller's `pgx.Tx`, writes outbound links and search queries, and returns new query ids. `RunSearch` reads ids from a channel, searches Brave, stores raw results in `discovery_search_queries.result`, then filters and queues kept URLs in `discovery_posts`.

**Tech Stack:** Go 1.27, pgx v5, text/template prompts, Brave Web Search, DeepSeek via `port.LLM`.

**Spec:** `docs/superpowers/specs/2026-11-08-event-driven-search-design.md`

## Global Constraints

- No new dependencies.
- `001_init.sql` is edited in place. Rework2 starts the database fresh.
- Go style per the user's CLAUDE.md: `any` over `interface{}`, guard clauses, default-then-override assignment, wrap leaf errors as `fmt.Errorf("err <what>: %w", err)`.
- Comments only for what code can't say. Match the existing terse style.
- Constants: events per post 5, tag keys in prompt 300, outbound links per post 10, dedupe window 7 days, daily budget 322, pending cap 200.
- Quality tag: `tags.type = 'quality' AND enabled AND status = 'active'`.
- Trusted post: its source has `enabled AND status = 'active'`.
- One commit per task. Never push.

## Review Focus

1. **Shutdown right after Brave billed.** Results must still be saved. Covered by `TestSearchSavesOnCancel` in Task 4.
2. **Same event twice in one post** with different query texts. Only one query is created. Covered by `TestDiscoverSameEventTwice` in Task 3.
3. **Brave returns zero results.** It counts as paid, `kept` is written as `[]` and the filter is not called. Covered by `TestSearchNoResults` in Task 4.
4. **Full channel.** `Enqueue` never blocks. Covered by `TestEnqueueFull` in Task 4.
5. **Non-ASCII entities** such as `Ålandsbanken` keep their letters in `normalized_entity`. Covered by `TestNormalizedEntity` in Task 3.

## Integration tests

Store SQL is tested against the local `kharoborathus-db` container, which has the Swedish dictionary files the schema needs. Tests skip unless `TEST_POSTGRES_CONN` is set, for example `postgres://user:pass@127.0.0.1:5432/postgres`. The URL form is required.

Run integration tests with:

```bash
TEST_POSTGRES_CONN=postgres://... go test ./internal/adapter/search/ -run 'Discover|Store|Search' -v
```

---

### Task 1: Events prompt contract, drop the step 2 ticker

**Files:**
- Modify: `internal/adapter/search/prompts/discovery.md`
- Modify: `internal/adapter/search/prompts.go`
- Modify: `internal/adapter/search/keywords.go`, `internal/adapter/search/keywords_test.go`
- Delete: `internal/adapter/search/discovery.go`, `internal/adapter/search/discovery_test.go`
- Modify: `cmd/main.go`

**Interfaces:**
- Produces: `Keyword{Entity, Action, Query string}`, `groundKeywords(keywords []Keyword, article string) []Keyword`, `tagKey(s string) string`, `collapse(s string) string`, `var EventsSystemPrompt string`, `func EventsUserPrompt(keys []string) (string, error)`.

- [ ] **Step 1: Rewrite the tests**

In `keywords_test.go`, drop `TestKeywords` and `TestKeywordsNone`, which use `Discoverer`. Keep `fakeLLM` and `article`, since `result_filter_test.go` uses them. Add:

```go
func TestGroundKeywords(t *testing.T) {
	// The input list of TestKeywords' fakeLLM answer, passed straight in.
	got := groundKeywords([]Keyword{ /* same 10 entries as the old TestKeywords */ }, article)
	// want: the same 5 entries the old TestKeywords expected, in order.
}

func TestEventsUserPrompt(t *testing.T) {
	keys := make([]string, 400)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
	}

	p, err := EventsUserPrompt(keys)
	// err == nil, p contains "Quality tag keys: k0, k1", contains "k299", not "k300".
}

func TestEventsUserPromptNone(t *testing.T) {
	// EventsUserPrompt(nil) contains "Quality tag keys: none".
}

func TestEventsSystemPrompt(t *testing.T) {
	// EventsSystemPrompt contains "events:" and not "keywords:".
}
```

- [ ] **Step 2: Run, expect a compile failure**

Run: `go test ./internal/adapter/search/ -run 'Ground|Events'`
Expected: FAIL, `undefined: EventsUserPrompt`.

- [ ] **Step 3: Implement**

- `prompts/discovery.md`: rename the defines to `events_system_prompt` and `events_user_prompt`. The system prompt drops "Read one article" framing in favour of "From the article, list...", since step 2 shares the article with other tasks. In the Response section, `keywords:` becomes `events:`. The user prompt keeps only the `Quality tag keys` line. The article is step 2's.
- `prompts.go`: `var EventsSystemPrompt = mustRender("events_system_prompt")`. Add `EventsUserPrompt(keys []string) (string, error)` that renders `events_user_prompt` with `keys[:min(len(keys), maxTagKeys)]`. Remove `discoverySystem`.
- `keywords.go`: delete `discoveryAnswer`, `Discoverer`, `NewDiscoverer`, `Keywords` and `maxDiscoveryInput`. Keep the rest.
- Delete `discovery.go` and `discovery_test.go`.
- `cmd/main.go`: remove `discoverer` and the `discovery.Run` goroutine.

- [ ] **Step 4: Verify**

Run: `go build ./... && go test ./internal/adapter/search/`
Expected: build OK, tests PASS.

- [ ] **Step 5: Commit**

```bash
git add -A internal/adapter/search cmd/main.go
git commit -m "Hand event extraction to step 2 as a prompt contract"
```

---

### Task 2: Outbound links from markdown

**Files:**
- Modify: `internal/adapter/search/links.go`, `internal/adapter/search/links_test.go`

**Interfaces:**
- Consumes: `filterLinks(raw []string, ownBaseURL string) []string`.
- Produces: `const maxLinksPerPost = 10`, `func outboundLinks(markdown, ownBaseURL string) []string`.

- [ ] **Step 1: Write the failing test**

```go
func TestOutboundLinks(t *testing.T) {
	md := `[a](https://news.example/a?utm_source=x) ![img](https://img.example/i.png)
[own](https://own.example/x) [t](<https://other.example/b> "title")`
	// want: ["https://news.example/a", "https://other.example/b"]
}

func TestOutboundLinksCap(t *testing.T) {
	// 15 distinct links to n%d.example → len(got) == 10, got[0] is n0.
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/adapter/search/ -run Outbound`
Expected: FAIL, `undefined: outboundLinks`.

- [ ] **Step 3: Implement `outboundLinks` in `links.go`**

Restore the regex from the old `internal/service/ingest/links.go`, at `git show ce33e9a^:internal/service/ingest/links.go`:

```go
var markdownLink = regexp.MustCompile(`(!?)\[[^\[\]]*\]\(<?([^\s()<>]+)>?(?:\s+"[^"]*")?\)`)
```

Skip matches where group 1 is `!`, run `filterLinks`, cap at `maxLinksPerPost`.

- [ ] **Step 4: Verify**

Run: `go test ./internal/adapter/search/ -run 'Outbound|FilterLinks|HostOf'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/search/links.go internal/adapter/search/links_test.go
git commit -m "Extract outbound links from post markdown"
```

---

### Task 3: Step 3, `Discover`

**Files:**
- Modify: `internal/migrations/001_init.sql`
- Create: `internal/adapter/search/discover.go`, `internal/adapter/search/discover_test.go`
- Modify: `internal/adapter/search/store.go`. Delete every old method in Task 4. In this task, add only the tx functions below.
- Create: `internal/adapter/search/db_test.go`

**Interfaces:**
- Consumes: `groundKeywords`, `tagKey`, `outboundLinks`, `domain.Post`.
- Note: `Discover` runs `groundKeywords` itself, so step 2 can't pass ungrounded events. The spec has step 2 run it. Same result, one less export.
- Produces:
  - `func Discover(ctx context.Context, tx pgx.Tx, post domain.Post, events []Keyword) ([]string, error)`
  - `func resolveAction(action string, quality map[string]bool, aliases map[string]string) (string, bool)`
  - `func normalizedEntity(entity string) string`, which is `tagKey(entity)`.
  - Test helpers `testDB(t *testing.T) *database.Database` and `seedPost(t *testing.T, db *database.Database, p seed) domain.Post`.

- [ ] **Step 1: Schema**

In `001_init.sql`, replace `-- TODO: Correctly handle tag aliases here.` with the `tag_aliases` DDL from the spec, placed after `tags`.

- [ ] **Step 2: Test harness in `db_test.go`**

- `testDB`:
  - Skip when `TEST_POSTGRES_CONN` is empty.
  - Otherwise `CREATE DATABASE kharo_test_<random hex>` over the admin connection.
  - Swap the URL path to that name and run `migrations.Migrate`.
  - Open `database.NewDatabase`.
  - `t.Cleanup` closes the pool and runs `DROP DATABASE ... WITH (FORCE)`.
- `seed` struct: `Trusted bool`, `Content string`, `BaseURL string`, `URL string`, `Age time.Duration`. It inserts a domain, a source with `enabled = Trusted`, `status = 'active'`, and a post with `created_at = now() - Age`. It returns the post with its ids filled.
- Also add `addTag(t, db, key, typ string)` and `addAlias(t, db, alias, key string)`.

- [ ] **Step 3: Write the failing tests**

Pure tests, no DB:

```go
func TestResolveAction(t *testing.T) {
	quality := map[string]bool{"acquisition": true}
	aliases := map[string]string{"takeover": "acquisition"}
	// "Take-over" → ("acquisition", true); "acquisition" → ("acquisition", true)
	// "new_factory" → ("", false)
}

func TestNormalizedEntity(t *testing.T) {
	// "Volvo  Cars" → "volvo_cars"; "Ålandsbanken" → "ålandsbanken"
}
```

DB tests, each starting with `db := testDB(t)` and running `Discover` in `pgx.BeginFunc`. Seed the quality tag `acquisition` and the alias `takeover → acquisition`. Article: `"Volvo Cars köper Northvolt. [l](https://news.example/a)"`.

| Test | Setup | Assert |
|---|---|---|
| `TestDiscoverNewQuery` | trusted post, event `{Volvo Cars, takeover, Volvo Cars köper Northvolt}` | 1 id returned. Row has `normalized_entity = 'volvo_cars'`, `action = 'acquisition'`. One `discovery_search_query_posts` row. One `discovery_posts` row with URL `https://news.example/a` and NULL query id. |
| `TestDiscoverSameEvent` | a second trusted post within 7 days, same entity and action, different query | 0 ids. Post linked to the first query. Still 1 query row. |
| `TestDiscoverOldEvent` | first query `created_at` set 8 days back | 1 new id. |
| `TestDiscoverQueryConflict` | an existing query with the same text, older than 7 days | 0 ids. Post linked to the existing row. |
| `TestDiscoverSameEventTwice` | one post, two events with the same entity and action, different queries | 1 id. 1 query row. |
| `TestDiscoverUntrusted` | untrusted post, a fresh event, plus an event matching an existing query | 0 ids. No `discovery_posts`. Linked to the existing query only. |
| `TestDiscoverNotQuality` | action `new_factory` | 0 ids, no query rows. |
| `TestDiscoverLinkAlreadyPost` | a post already exists with URL `https://news.example/a` | No `discovery_posts` row. |

- [ ] **Step 4: Run, expect failure**

Run: `TEST_POSTGRES_CONN=... go test ./internal/adapter/search/ -run 'Discover|ResolveAction|NormalizedEntity'`
Expected: FAIL, `undefined: Discover`.

- [ ] **Step 5: Implement**

`discover.go`, `Discover`:
1. `postSource(ctx, tx, post)` returns `trusted bool` and `baseURL string` from `sources` joined with `domains` on `post.DomainID`.
2. If trusted, `insertLinks(ctx, tx, post.ID, outboundLinks(post.Content, baseURL))`.
3. `actionKeys(ctx, tx)` returns `quality map[string]bool` and `aliases map[string]string`.
4. For each event in `groundKeywords(events, post.Content)`:
   - Run `resolveAction`. On `!ok`, continue.
   - `entity := normalizedEntity(e.Entity)`.
   - `recentQuery(ctx, tx, entity, action, dedupeWindow)` with `created_at > now() - make_interval(days => $3)`. If found, `linkQuery`, then continue.
   - If not trusted, continue.
   - `insertQuery(ctx, tx, entity, action, e.Query)` uses `INSERT ... ON CONFLICT (query) DO NOTHING RETURNING id`. On no row, select the existing id by `query` and return `created = false`.
   - `linkQuery`. If created, append the id.

`store.go` gets these tx functions, each wrapping its error:
- `postSource`, `insertLinks`, `actionKeys`, `recentQuery`, `insertQuery`, `linkQuery`.
- `insertLinks` uses the old `unnest` insert, retargeted to `discovery_posts (url, from_post_id)`. It keeps `WHERE NOT EXISTS (SELECT 1 FROM posts p WHERE p.url = u)` and `ON CONFLICT (url) DO NOTHING`.
- `linkQuery` uses `ON CONFLICT DO NOTHING`.

- [ ] **Step 6: Verify**

Run: `TEST_POSTGRES_CONN=... go test ./internal/adapter/search/ -v -run 'Discover|ResolveAction|NormalizedEntity'`
Expected: PASS. Without the env var, the DB tests SKIP and the pure tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/migrations/001_init.sql internal/adapter/search
git commit -m "Discover links and search queries inside the post transaction"
```

---

### Task 4: Step 4, event-driven `RunSearch`

**Files:**
- Modify: `internal/adapter/search/search.go`, `internal/adapter/search/search_test.go`
- Modify: `internal/adapter/search/store.go`. Delete `ListUndiscoveredPosts`, `CompleteDiscovery`, `Actions`, `ListQueue`, `Searched` and `MarkSearched`.
- Modify: `internal/adapter/search/model.go`
- Modify: `internal/adapter/search/result_filter.go`, `internal/adapter/search/result_filter_test.go`
- Modify: `cmd/main.go`
- Modify: `docs/kb/search-discovery.md`

**Interfaces:**
- Consumes: `Searcher`, `Query`, `SearchResult`, `errSearchRejected`, `filterLinks`, `ResultFilter`.
- Produces:
  - `const SearchQueue = 100`
  - `func Enqueue(ids chan<- string, l *slog.Logger, id string) bool`
  - `func RunSearch(ctx context.Context, s searchStore, ids <-chan string, se Searcher, f *ResultFilter, sched schedule.Schedule, l, calls *slog.Logger) error`
  - `func searchQuery(ctx context.Context, s searchStore, id string, se Searcher, f *ResultFilter, sched schedule.Schedule, l, calls *slog.Logger) error`
  - `func (f *ResultFilter) Keep(ctx, l, entity, action string, results []SearchResult) ([]int, error)`, which now returns indexes.
  - `type SearchItem struct { ID, Entity, Action, Query, Lang, BaseURL, FromPostID string; SearchedAt *time.Time }`
  - `type searchStore interface`, which `*Store` satisfies:
    - `GetQuery(ctx context.Context, id string) (SearchItem, error)`
    - `PendingResults(ctx context.Context, windowDays int) (int, error)`
    - `SearchesLastDay(ctx context.Context) (int, error)`
    - `MarkRejected(ctx context.Context, id string) error`
    - `SaveResults(ctx context.Context, id string, results []SearchResult) error`
    - `SaveKept(ctx context.Context, item SearchItem, kept []int, urls []string) error`

- [ ] **Step 1: Write the failing tests**

`result_filter_test.go`: `TestKeep` now expects `[]int{2, 0}`. That means a valid answer, deduped by URL, in model order. `TestKeepNoResults` expects an empty slice.

`search_test.go`: delete `TestNextPause`. Add a `fakeStore` implementing `searchStore`. It records calls and returns configurable pending and used counts, plus a fixed `SearchItem`. Add a `fakeSearcher` returning configurable results or an error. Test `searchQuery` directly:

| Test | Setup | Assert |
|---|---|---|
| `TestSearchSkipsSearched` | `SearchedAt` set | Searcher not called. |
| `TestSearchPendingCap` | pending 200 | Searcher not called, no writes. |
| `TestSearchBudget` | used 322 | Searcher not called, no writes. |
| `TestSearchRejected` | searcher returns `errSearchRejected` | `MarkRejected` only. |
| `TestSearchUnavailable` | `port.ErrUnavailable`, then a plain error such as a 401 | No writes in either case. |
| `TestSearchUnreadable` | `errSearchUnreadable` | No writes. |
| `TestSearchFiltered` | 3 results, filter keeps `[2, 0]` | `SaveResults` with 3, then `SaveKept` with `[2, 0]` and the two URLs. Filter entity is `volvo cars` for `volvo_cars`. |
| `TestSearchClosedSchedule` | schedule never active | `SaveResults` only. LLM not called. |
| `TestSearchFilterError` | LLM errors | `SaveResults` only. |
| `TestSearchNoResults` | searcher returns empty | `SaveResults` with 0, `SaveKept` with `[]`. LLM not called. |
| `TestSearchSavesOnCancel` | ctx cancelled inside the searcher | `SaveResults` still called. |
| `TestEnqueueFull` | `make(chan string, 1)`, two `Enqueue` calls | `true`, then `false`, without blocking. |

DB test `TestStoreSearch`:
- Seed a query linked to two posts, the earlier one with lang `sv`.
- `GetQuery` returns lang `sv` and the earlier post's id.
- After `SaveResults`, `SearchesLastDay == 1`.
- After `SaveKept` with one URL:
  - `PendingResults == 1`,
  - `result->'kept' = [0]`,
  - the stored result JSON has `url` on each entry.
- `MarkRejected` on a fresh query sets `searched_at` and leaves `result` NULL, with `SearchesLastDay` unchanged.

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/adapter/search/ -run 'Search|Keep|Enqueue'`
Expected: FAIL, compile errors on `searchQuery`, `Enqueue` and `[]int`.

- [ ] **Step 3: Implement**

- `result_filter.go`: `Keep` returns indexes. Same validation as now, deduped by URL.
- `model.go`: drop `DiscoverItem` and `Post`. `SearchItem` as above. Add `storedResult struct { URL string \`json:"url"\`; SearchResult }` for the `result.results` entries.
- `search.go`:
  - Keep `dedupeWindow`, `dailySearchBudget` and `maxPendingResults`. Delete `searchInterval`, `searchBatch`, `wastedPause`, `maxWastedPause`, `errSearchWasted`, `nextPause` and `recordWasted`.
  - `Enqueue` is a non-blocking `select` with `default`. When it drops an id, it logs a warning with `"query", id`.
  - `RunSearch` ranges over `ids` until ctx is done, calls `searchQuery` and logs its error.
  - `searchQuery` follows spec step 4 in order:
    - Use `calls.With("discovery_search_query_id", id)` for the Brave and LLM logger.
    - `SaveResults` and `SaveKept` use `context.WithoutCancel(ctx)`.
    - The filter runs only if `sched.Active(time.Now())`.
    - The filter entity is `strings.ReplaceAll(item.Entity, "_", " ")`.
- `store.go`, `Store` methods:
  - `GetQuery`: select the query with a `LATERAL` join to its earliest linked post by `p.created_at`, for `lang`, `id` and `domains.base_url`.
  - `PendingResults`: count `discovery_posts` that are `pending`, have `discovery_search_query_id IS NOT NULL` and were created within the window.
  - `SearchesLastDay`: count rows with `searched_at > now() - interval '1 day' AND result IS NOT NULL`.
  - `MarkRejected`: set `searched_at = now()`.
  - `SaveResults`: set `searched_at = now()` and `result = jsonb_build_object('results', $2::jsonb)`.
  - `SaveKept`: one tx. Set `result = jsonb_set(result, '{kept}', $2::jsonb)`. Insert `urls` into `discovery_posts (url, from_post_id, discovery_search_query_id)` with the same not-a-post and conflict rules as `insertLinks`.
- `cmd/main.go`: `searchIDs := make(chan string, discovery.SearchQueue)`. The search goroutine calls `discovery.RunSearch(ctx, &discoveryStore, searchIDs, searcher, resultFilter, llm.Schedule(), l.WithGroup("search"), calls)`. Its `searchErr` guard is unchanged.
- `docs/kb/search-discovery.md`: rewrite Current state and Design to match the spec: step 3 inside the post transaction, step 4 on query ids, the `result` states table, and no tickers or switches. Keep the template headings.

- [ ] **Step 4: Verify**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS.
Run: `TEST_POSTGRES_CONN=... go test ./internal/adapter/search/ -v`
Expected: all PASS, none skipped.

- [ ] **Step 5: Commit**

```bash
git add -A internal/adapter/search cmd/main.go docs/kb/search-discovery.md
git commit -m "Search Brave per query id instead of on a ticker"
```
