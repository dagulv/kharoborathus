# Backfill Strategies Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Backfill a source's history through its adapter's strategies, for rss and page sources, when the source is created, approved, gets its rules, or a button is pressed. Plain HTTP only, no headless browser. Fetch every post's article page, and keep the fetch path free of DeepSeek, Jev and Brave calls.

**Architecture:**
- **Backfill as a strategy.** An adapter that can read history implements `port.Backfiller`. `rss` implements it with an ordered list of strategies; WordPress, found through `FindWordPress` with the rss adapter's pacer, is the first. The `wordpress` adapter type goes away. `page` implements it too, with WordPress, `rel="next"`, URL patterns and a DeepSeek-learned pagination rule.
- **Rule events.** Saving a `links` or `pages` rule sends the domain id, and that domain's page sources backfill.
- **Backfill trigger.** An admin endpoint sends the source on a Go channel. A backfill `route` hands it to a per-adapter backfill plugin with its own workers, outside `sourceStream` and the live `route`.
- **Article pages.** `rss.FetchPost` always fetches the article page. `Service.extract` keeps the longer of the page and feed content.
- **Off the fetch path.** Old posts create no search queries, discovered ingest loses its daily cap, and the one-listing link rule runs in the background.

**Tech Stack:** Go 1.27, pgx v5, gofeed, goquery, SvelteKit remote functions with `postgres`.

**Spec:** `docs/superpowers/specs/2026-11-12-backfill-on-creation-design.md`

## Global Constraints

- No schema changes. No new migration file.
- Every request to a host goes through the shared `*pace.Pacer` from `cmd/main.go`, with a gap of `source.DomainPace`, 5 s.
- No polling for backfill work, and no Postgres LISTEN or NOTIFY.
- Jev stays uncapped. `maxPendingResults` stays at 200.
- Go style follows `~/.claude/CLAUDE.md`:
  - Use `any`, never `interface{}`.
  - Errors at leaf calls are wrapped as `fmt.Errorf("err <what>: %w", err)`.
  - Use guard clauses, and assign the default value first.
  - Comment only non-obvious intent.
- One commit per task. Do not push.
- DB tests skip unless `TEST_POSTGRES_CONN` holds a Postgres connection string with rights to create databases. Run them with it set.

## Review Focus

- **A `rss` source whose URL is a feed.** `FindWordPress` must find the API from the feed's `Link` header. Tested in Task 4.
- **Archive posts land on the real source.** Tested in Task 5 by `TestFetcherBackfill`, through the `"a ..."` source id.
- **A listing that answers page 1 for every `?page=N`.** The run must stop after page 1, not loop forever. Tested in Task 11.
- **Full backfill queue.** The endpoint answers `503`, and `createSource` still redirects. Tested in Task 5 for Go; checked by hand for the form in Task 6.
- **Post with no posted date.** It counts as new, so it can still start a search. Tested in Task 3.
- **Feed content heavy in markup but short in text.** It must not beat a longer page article. Tested in Task 2.

---

### Task 1: RSS always fetches the article page

**Files:**
- Modify: `internal/adapter/fetcher/rss.go`
- Test: `internal/adapter/fetcher/rss_test.go`

**Interfaces:**
- Produces: `rss.FetchPost(ctx, l, data rssDocument) (rssDocument, error)` fetches every item's page into `data.Page`. A failed page fetch logs at debug and returns `data, nil`, keeping the feed fields. It returns an error only from `pacer.Wait`. Archive items from Task 4 go through the same `FetchPost`.

- [ ] **Step 1: Replace `TestRSSFetchesArticleOfExcerptItem` with `TestRSSFetchesEveryArticle`**
  - `rssServer`: drop the `t.Error` on `/full` and serve `<html><body><article>full page</article></body></html>` there.
  - Add a third item `Gone` linking to `srv.URL+"/gone"`, which answers 404, with `<content:encoded><![CDATA[<p>feed only</p>]]>`.
  - Assert, by title:
    - `RawContent["Excerpt"]` is the excerpt page HTML.
    - `RawContent["Full"]` is the full page HTML, and its `Content` is still `<p>whole article</p>`.
    - `RawContent["Gone"] == ""`, `Content["Gone"] == "<p>feed only</p>"`, and `FetchPost` returned no error for it.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/adapter/fetcher/ -run TestRSSFetchesEveryArticle -v`
Expected: FAIL. `RawContent["Full"]` is empty, and `Gone` returns an error.

- [ ] **Step 3: Implement**
  - Remove the `excerpt` field from `rssDocument` and its assignment in `FetchFeed`. Keep the `item.Description` fallback for `Content`.
  - In `FetchPost`, drop the excerpt guard.
  - On a `FetchHTML` error, call `l.Debug("fetch article", "url", data.URL, "err", err)` and return `data, nil`.
  - Update the comment above `FetchPost`.

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/adapter/fetcher/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/fetcher/rss.go internal/adapter/fetcher/rss_test.go
git commit -m "Fetch the article page of every feed item"
```

### Task 2: Extract keeps the longer content

**Files:**
- Modify: `internal/core/service/source/extract.go`
- Test: `internal/core/service/source/extract_test.go`

**Interfaces:**
- Produces: `plainLen(html string) int`, the rune count of the text goquery reads from `html`, in `extract.go`.
- Produces: `Service.extract` replaces `p.Content` with the extracted content only when `plainLen(extracted) > plainLen(p.Content)`.

- [ ] **Step 1: Write `TestExtractKeepsLongerContent`**

Use a `fakeExtractor` whose `Content` returns `"extracted"`, 9 runes. Three cases on `s.extract(..., domain.RawPost{RawContent: "<html></html>", Content: c})`:

| `c` | Want `p.Content` |
|---|---|
| `""` | `"extracted"` |
| `"<p>feed article that is longer</p>"` | that string |
| `"<div class=\"a very long class list here\"><b>x</b></div>"` | `"extracted"` |

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/core/service/source/ -run TestExtractKeepsLongerContent -v`
Expected: FAIL on the second case.

- [ ] **Step 3: Implement `plainLen` and the guard in `Service.extract`**

`plainLen` returns 0 when goquery cannot parse. Update the comment on `Extract` to say the feed content wins when it is longer.

- [ ] **Step 4: Run the package tests**

Run: `go test ./internal/core/service/source/ -run 'TestExtract' -v`
Expected: PASS, including `TestExtractKeepsFetched`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/service/source/extract.go internal/core/service/source/extract_test.go
git commit -m "Keep feed content when it is longer than the page's"
```

### Task 3: Old posts start no search

**Files:**
- Modify: `internal/adapter/search/discover.go`
- Test: `internal/adapter/search/discover_test.go`

**Interfaces:**
- Consumes: `dedupeWindow = 7` (days) from `search.go`.
- Produces: in `Discover`, the guard before `insertQuery` becomes `if !trusted || old(post) { continue }`. `old(post domain.Post) bool` is true when `post.PostedAt != nil` and it is before `time.Now().AddDate(0, 0, -dedupeWindow)`.

- [ ] **Step 1: Write three tests. Set `post.PostedAt` on the returned `domain.Post` before calling `discoverAll`.**
  - **`TestDiscoverOldPost`:** a trusted post with `volvoArticle`, `PostedAt` 8 days ago, and `volvoTakeover`. Assert:
    - `f.Queries` is empty.
    - `SELECT count(*) FROM discovery_search_queries` is 0.
    - `len(f.Links) == 1`.
  - **`TestDiscoverOldPostJoinsEvent`:** first a fresh trusted post with `volvoTakeover`, which creates one query. Then an old post with the same event. Assert:
    - `discovery_search_queries` count is 1.
    - `discovery_search_query_posts` has a row for the old post on the first query.
  - **`TestDiscoverUndatedPost`:** `PostedAt` nil. Assert one query id is returned.

- [ ] **Step 2: Run them to verify they fail**

Run: `TEST_POSTGRES_CONN=... go test ./internal/adapter/search/ -run 'TestDiscover(OldPost|UndatedPost)' -v`
Expected: `TestDiscoverOldPost` FAILS with one query. The other two pass.

- [ ] **Step 3: Implement `old` and the guard**

Extend the comment above `Discover`: "Untrusted and old posts only join events a trusted, recent post already found."

- [ ] **Step 4: Run the package tests**

Run: `TEST_POSTGRES_CONN=... go test ./internal/adapter/search/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/search/discover.go internal/adapter/search/discover_test.go
git commit -m "Start no search for posts older than the dedupe window"
```

### Task 4: WordPress as an rss backfill strategy

**Files:**
- Modify: `internal/core/port/fetcher.go`
- Modify: `internal/adapter/fetcher/rss.go`
- Modify: `internal/adapter/fetcher/wordpress.go`
- Test: `internal/adapter/fetcher/wordpress_test.go`

**Interfaces:**
- Produces in `port`:

```go
var ErrNoArchive = errors.New("no archive")

// An adapter that can read a source's history. Items run through the adapter's Filter, FetchPost and ProcessFeed.
type Backfiller[T any] interface {
	Backfill(ctx context.Context, l *slog.Logger, url string) (<-chan T, error)
}
```

- Produces in `fetcher`:

```go
// archive streams a feed's history. ok is false when the site does not support it.
type archive func(ctx context.Context, l *slog.Logger, url string) (items <-chan rssDocument, ok bool, err error)
```

  - `rss` gains `archives []archive`. `NewRSS` builds the `rss` value, sets `r.archives = []archive{r.wordpress}`, and returns it.
  - `func (r rss) Backfill(ctx, l, url string) (<-chan rssDocument, error)` tries `r.archives` in order. It returns the first one with `ok`, the first error, or `port.ErrNoArchive`. Add `var _ port.Backfiller[rssDocument] = rss{}`.
  - `func wordpressArchive(ctx context.Context, client *http.Client, pacer *pace.Pacer, l *slog.Logger, url string) (<-chan rssDocument, bool, error)` is a package function, so `page` can reuse it in Task 11. `func (r rss) wordpress(ctx, l, url)` calls it with `logs.Client(r.client, l, "wordpress")` and `r.pacer`. `wordpressArchive`:
    - It calls `FindWordPress(ctx, client, pacer, url)`.
    - A detection error logs `l.Warn("find wordpress", "url", url, "err", err)` and returns `ok` false. That covers a closed API answering 401 or 404.
    - An empty `wp.Posts` returns `ok` false.
    - Otherwise it reads page 1 synchronously, as `wordpress.FetchFeed` does now. An error there returns `nil, true, err`. The other pages stream from a goroutine. Every document gets `Lang = wp.Lang`.
  - Delete the `wordpress` type, `NewWordPress` and `var _ port.Fetcher[rssDocument] = wordpress{}`. Keep `FindWordPress`, `page` and `document`, now as methods or functions on `rss`.

- [ ] **Step 1: Write the tests in `wordpress_test.go`**
  - **Rename `TestWordPressFetchesEveryPage` to `TestRSSBackfillWordPress`.**
    - Build `r := rss{client: srv.Client(), pacer: pace.New(gap), l: l}`, then `r.archives = []archive{r.wordpress}`.
    - Call `r.Backfill(ctx, l, srv.URL+"/feed")`.
    - Keep every existing assertion, and add that every document has `Lang == "sv-SE"`.
  - **`TestRSSBackfillOrder`.** Set `r.archives` to three fakes:
    1. One that returns `ok` false.
    2. One that returns a channel with `rssDocument{URL: "x"}`.
    3. One that calls `t.Error`.

    Assert the only document read is `x`.
  - **`TestRSSBackfillNone`.** Use the WordPress strategy on `srv.URL+"/plain"`. Assert `errors.Is(err, port.ErrNoArchive)`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/adapter/fetcher/ -run 'TestRSSBackfill' -v`
Expected: FAIL to compile, since `archive` and `Backfill` are undefined.

- [ ] **Step 3: Implement `port.Backfiller`, `ErrNoArchive`, `rss.Backfill` and `rss.wordpress`. Delete the `wordpress` adapter.**

`cmd/main.go` stops compiling until Task 5. Remove its `"wordpress"` entry now so the build stays green.

- [ ] **Step 4: Run the package tests and build**

Run: `go build ./... && go test ./internal/adapter/fetcher/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/port/fetcher.go internal/adapter/fetcher/rss.go internal/adapter/fetcher/wordpress.go internal/adapter/fetcher/wordpress_test.go cmd/main.go
git commit -m "Read a WordPress archive as a backfill strategy of the rss adapter"
```

### Task 5: Backfill lanes, endpoint and wiring

**Files:**
- Modify: `internal/core/service/source/fetch.go`
- Modify: `internal/core/service/source/sourced.go`
- Modify: `internal/core/service/source/route.go`
- Modify: `cmd/main.go`
- Modify: `docs/kb/backfill.md`
- Test: `internal/core/service/source/fetch_test.go`
- Test: `internal/core/service/source/route_test.go`

**Interfaces:**
- Consumes: `port.Backfiller[T]` and `port.ErrNoArchive` from Task 4.
- Produces:
  - **`Fetcher` interface in `sourced.go`** gains:

    ```go
    // Backfill is false when the adapter has no backfill strategy.
    Backfill(ctx context.Context) (pipeline.Pipeline[domain.Source, domain.RawPost], bool)
    ```

  - **`fetcher[T].Backfill`** type-asserts `p.Fetcher.(port.Backfiller[T])`. Its pipeline matches `Plugin` except that its first stage reads with `Backfill` instead of `FetchFeed`.
    - Make `listFeed` take the read function and a log name, so both pipelines share it.
    - `port.ErrNoArchive` logs `l.Info("no archive", "source", id)`. Other errors log as `fetchFeed` errors do now.
  - **`route`** becomes `route(ctx, sources <-chan domain.Source, ins map[string]chan domain.Source, l *slog.Logger, missing string)`. It logs `missing` for an adapter it has no input for. The live route passes `"unknown adapter"`, and the backfill route passes `"no backfill strategy"`.
  - **`Run`** becomes `func Run(ctx context.Context, s *Service, fetchers map[string]Fetcher, backfills <-chan domain.Source, l *slog.Logger) error`. For each fetcher whose `Backfill` is ok, it makes a backfill input and merges `Stream(ctx, l.With("adapter", name, "backfill", true), in)` into the posts. A second `go route(ctx, backfills, backIns, l, "no backfill strategy")` feeds those inputs.
  - **`BackfillQueue = 16`** goes next to `SyncQueue` in `route.go`.
  - **`Routes`** becomes `func Routes(mux *http.ServeMux, s *postgres.SourceStore, syncs, backfills chan<- domain.Source, l, calls *slog.Logger)` and registers `POST /admin/sources/{id}/backfill`.
  - **`syncSource`** becomes `queueSource(s *postgres.SourceStore, queue chan<- domain.Source, name string, l *slog.Logger) http.HandlerFunc`, used by both routes with `name` set to `"sync"` or `"backfill"`.
    - The full-queue body reads `name + " queue full"`.
    - Status codes are unchanged: `404`, `409`, `202`, `503`.

- [ ] **Step 1: Write the tests**
  - **`fakeBackfiller` in `fetch_test.go`.** It embeds `fakeFetcher` and adds `archive map[string][]string`. Its `Backfill` returns the archive items, or `port.ErrNoArchive` for an unknown URL. It also sends each URL it is called with on a `called chan string`, when that is set.
  - **`TestFetcherBackfill`:**
    - `NewFetcher(fakeFetcher{}, discard).Backfill(ctx)` reports false.
    - `NewFetcher` with a `fakeBackfiller` whose feed is `{"https://a.example/feed": {"https://a.example/new"}}` and archive is `{"https://a.example/feed": {"https://a.example/old", "https://a.example/seen"}}` reports true.
    - Streaming source `{ID: "a", URL: "https://a.example/feed"}` yields exactly `"a https://a.example/old"`.
  - **`TestRunFeedsBackfillPlugin`:**
    - Use `qualityServiceDB(t, nil)`. Its source is disabled, so `sourceStream` sends nothing.
    - Call `Run(ctx, s, map[string]Fetcher{"rss": NewFetcher(backfiller, discard)}, backfills, l)` in a goroutine. `l` writes to a buffer.
    - Send `{ID: "x", Adapter: "rss", URL: "https://a.example/feed"}` and `{ID: "y", Adapter: "page", URL: "https://b.example/"}` on `backfills`.
    - Assert `called` receives `https://a.example/feed` within 2 s.
    - Assert the log contains `no backfill strategy` with `adapter=page`. Then cancel `ctx`.
  - **`TestQueueSource`.** Turn `TestSyncSource` into it. It loops over `"sync"` and `"backfill"`, keeps every existing assertion, and checks for `name + " queue full"` in the body on the `503`.
  - **`TestRoutesUnderAdmin`.** Add `{http.MethodPost, "/admin/sources/nope/backfill", http.StatusNotFound}`, and pass a second channel to `Routes`.

- [ ] **Step 2: Run them to verify they fail**

Run: `TEST_POSTGRES_CONN=... go test ./internal/core/service/source/ -run 'TestFetcherBackfill|TestRunFeedsBackfillPlugin|TestQueueSource|TestRoutesUnderAdmin' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `fetcher[T].Backfill`, the shared `listFeed`, `route`, `Run`, `queueSource` and `Routes`**

- [ ] **Step 4: Wire `cmd/main.go`**
  - Add `backfills := make(chan domain.Source, source.BackfillQueue)` next to `syncs`.
  - Pass it to `source.Routes` and to `source.Run`.

- [ ] **Step 5: Run every Go test and build**

Run: `go build ./... && TEST_POSTGRES_CONN=... go test ./...`
Expected: PASS.

- [ ] **Step 6: Rewrite `docs/kb/backfill.md`**

Match the spec:
- **What backfill is:** a strategy of the `rss` adapter, through `port.Backfiller`, with WordPress as its first strategy.
- **Trigger:** creation, approval of a proposed source, and the button.
- **No state.** A rerun skips stored posts.
- **Separate lanes**, and `page` sources do not backfill.
- **Paths:** `internal/adapter/fetcher/wordpress.go`, `internal/core/service/source/fetch.go` and `sourced.go`.
- **Removed:** the `wordpress` adapter, `backfilled_at`, the batch of 10 and `RunSummaries`.

- [ ] **Step 7: Commit**

```bash
git add internal/core/service/source/fetch.go internal/core/service/source/sourced.go internal/core/service/source/route.go internal/core/service/source/fetch_test.go internal/core/service/source/route_test.go cmd/main.go docs/kb/backfill.md
git commit -m "Run backfills in their own lane per adapter from an admin endpoint"
```

### Task 6: Admin triggers

**Files:**
- Modify: `frontend/src/routes/admin/admin.remote.ts`
- Modify: `frontend/src/routes/admin/sources/+page.svelte`

**Interfaces:**
- Consumes: `POST /admin/sources/{id}/backfill` from Task 5.
- Produces:
  - `requestBackfill(id: string): Promise<void>`, an unexported helper. It POSTs with `goAuth()` and throws an `error` with the same status mapping as `syncSource`. The `409` text reads "Source is not active or is disabled".
  - `export const backfillSource = command(uuid, requestBackfill)`.

- [ ] **Step 1: Change `createSource`**

Add `RETURNING id` to the source insert and capture the id from the transaction. After the transaction, call `await requestBackfill(id).catch((err) => console.error('backfill', id, err))`, then `redirect(303, '/admin/sources')`.

- [ ] **Step 2: Change `reviewSource`**

Return the status from before the update, and call `requestBackfill` when `enabled` is true and that status was `'proposed'`. Catch errors the same way. The SQL below is fixed:

```sql
UPDATE sources s
SET enabled = ${enabled}, status = CASE WHEN ${enabled} THEN 'active' ELSE s.status END
FROM (SELECT id, status FROM sources WHERE id = ${id}) old
WHERE s.id = old.id AND s.deleted_at IS NULL
RETURNING old.status AS was
```

- [ ] **Step 3: Add a Backfill button in `+page.svelte`**
  - Put it next to Sync, under the same `{#if}` guard.
  - Track its own state in `backfillState`, a `SvelteMap` like `syncState`.
  - Labels: `Backfilling…`, `Requested`, `Failed`, `Backfill`.
  - Title: `Read the source's history, skipping stored posts`.

Use the `svelte:svelte-file-editor` agent or the Svelte MCP autofixer on the `.svelte` file.

- [ ] **Step 4: Verify**

Run: `cd frontend && pnpm check && pnpm lint`
Expected: no errors.

Check by hand against a running stack:
1. Add an rss source for a WordPress site. The `logs` table shows `wordpress` requests, and old posts appear in `raw_posts`.
2. Press Backfill. It shows `Requested`, and stored posts are skipped.
3. Stop the Go app and add a source. The form still redirects.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/routes/admin/admin.remote.ts frontend/src/routes/admin/sources/+page.svelte
git commit -m "Backfill on source creation, approval and a button"
```

### Task 7: Discovered ingest without a daily cap

**Files:**
- Modify: `internal/core/service/source/discovered.go`
- Modify: `internal/adapter/postgres/discovered.go`
- Modify: `internal/adapter/search/search.go:19`
- Modify: `docs/kb/discovered-ingest.md`
- Test: `internal/core/service/source/discovered_test.go`

**Interfaces:**
- Produces: `Discovered.ingest` no longer reads a daily count. `dailyFetchCap` and `DiscoveredStore.CheckedLastDay` are deleted.

- [ ] **Step 1: Replace `TestIngestDailyCap` with `TestIngestNoDailyCap`**

Seed 200 rows checked today, as the old test does, with a literal `200` instead of `dailyFetchCap`. Then queue one page and ingest it. Assert its status is `accepted`.

- [ ] **Step 2: Run it to verify it fails**

Run: `TEST_POSTGRES_CONN=... go test ./internal/core/service/source/ -run TestIngestNoDailyCap -v`
Expected: FAIL with status `pending`.

- [ ] **Step 3: Implement the removal**
  - Delete the cap check from `ingest`, `dailyFetchCap`, and `CheckedLastDay`.
  - Change the comment on `Discovered.Run` to "One link at a time." It no longer has a cap that could race.
  - Change the `maxPendingResults` comment in `search.go` to "More would age out before discovered ingest gets to them." The value stays `200`.
  - In `docs/kb/discovered-ingest.md`, drop design step 2 and the daily cap row.

- [ ] **Step 4: Run the package tests**

Run: `TEST_POSTGRES_CONN=... go test ./internal/core/service/source/ ./internal/adapter/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/service/source/discovered.go internal/core/service/source/discovered_test.go internal/adapter/postgres/discovered.go internal/adapter/search/search.go docs/kb/discovered-ingest.md
git commit -m "Drop the daily fetch cap from discovered ingest"
```

### Task 8: One-listing link rule off the fetch path

**Files:**
- Modify: `internal/adapter/extract/html/collect.go:182-238`
- Test: `internal/adapter/extract/html/collect_test.go:159-193`

**Interfaces:**
- Produces:
  - `linksFromOneListing` becomes `oneListing(ctx, l, d domain.Domain, s sample) bool`. It returns true when it started generation in a goroutine that holds `t.generating`. It returns false when it skipped, with the same skip rules as today: tried, failing, closed schedule or busy semaphore.
  - The goroutine does what the inline code did: on `port.ErrUnavailable` it returns, so it gets tried again; otherwise it sets `triedOne`. On error it calls `t.collectListing`. On success it calls `saveRule`.
  - `withoutLinkRule` calls `collectListing` only when `oneListing` returns false. It always returns `nil`.
  - The pipeline never waits on DeepSeek. The next rate tick uses the saved rule.

- [ ] **Step 1: Update the two tests**
  - **`TestLinksTriesOneListingFirst`:** assert `withoutLinkRule` returns nil. Call `wait(tr)`, then assert `llm.calls == len(temperatures)` and one listing kept. The second half is unchanged, with `wait(tr)` before its asserts.
  - **`TestLinksRetriesOneListingWhenUnavailable`:** call `wait(tr)` after each `withoutLinkRule`. Assert `llm.calls == 2`.
  - **Add `TestLinksOneListingSavesRule`:** a `fakeLLM` answering a valid `LinkRule` for `listings(1)[0]`, and a `fakeStore`. Assert:
    - `withoutLinkRule` returns nil.
    - After `wait(tr)`, the store holds key `links`.

Take the valid rule's selector from the fixtures already used in `links_test.go`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/adapter/extract/html/ -run 'TestLinks' -v`
Expected: `TestLinksOneListingSavesRule` FAILS, since links come back inline.

- [ ] **Step 3: Implement `oneListing` and the new `withoutLinkRule`**

Update the comment on it: "A new source gets its posts on the tick after the rule is saved."

- [ ] **Step 4: Run the package tests with the race detector**

Run: `go test -race ./internal/adapter/extract/html/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/extract/html/collect.go internal/adapter/extract/html/collect_test.go
git commit -m "Generate the one-listing link rule in the background"
```

### Task 9: Paced extractor fetches and rule events

**Files:**
- Modify: `internal/adapter/extract/html/extractor.go`
- Modify: `internal/adapter/extract/html/collect.go:240-247`
- Modify: `cmd/main.go`
- Modify: every `newExtractor(` call in `internal/adapter/extract/html/*_test.go`
- Test: `internal/adapter/extract/html/collect_test.go`

**Interfaces:**
- Produces: `func New(llm port.LLM, domains DomainStore, pacer *pace.Pacer, ruled chan<- string, l *slog.Logger) port.Extractor`, and `newExtractor` with the same parameters.
  - `t.fetcher(l)` calls `t.pacer.Wait(ctx, url)` before each fetch. A nil pacer never waits.
  - `saveRule` sends `domainID` on `t.ruled` after a successful save, only for `fieldLinks` and `fieldPages`. The send never blocks. On a full channel it logs `t.l.Warn("rule event dropped", "domain_id", domainID)`. A nil channel sends nothing.
  - `fieldPages field = "pages"` is added next to `fieldLinks`.

- [ ] **Step 1: Write the tests**

`TestSaveRuleSendsDomain`:
- `ruled := make(chan string, 1)`.
- `saveRule(ctx, "d", fieldLinks, LinkRule{})` puts `"d"` on `ruled`.
- `saveRule` for `fieldContent` sends nothing.
- A second links save while `ruled` is full returns within 100 ms.

`TestFetcherPaced`:
- An `httptest` server, and `pace.New(50 * time.Millisecond)`.
- Two calls to `t.fetcher(discard)` on the same host take at least 50 ms.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/adapter/extract/html/ -run 'TestSaveRuleSendsDomain|TestFetcherPaced' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

In `cmd/main.go`:
- Move `pacer := pace.New(source.DomainPace)` above `html.New`.
- Add `ruled := make(chan string, source.RuledQueue)`.
- Pass `pacer` and `ruled`.

`RuledQueue` comes from Task 12, so define `const RuledQueue = 16` in `route.go` now.

- [ ] **Step 4: Run the package tests and build**

Run: `go build ./... && go test -race ./internal/adapter/extract/html/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/extract/html cmd/main.go internal/core/service/source/route.go
git commit -m "Pace extractor fetches and send an event when a link rule is saved"
```

### Task 10: Pagination rule

**Files:**
- Create: `internal/adapter/extract/html/pages.go`
- Create: `internal/adapter/extract/html/prompts/pages_system.md`
- Create: `internal/adapter/extract/html/prompts/page_rule.md`
- Modify: `internal/adapter/extract/html/prompts.go`
- Modify: `internal/adapter/extract/html/collect.go`
- Modify: `internal/core/port/extract.go`
- Modify: `internal/core/service/source/extract_test.go`, adding `Next` to `fakeExtractor`
- Test: `internal/adapter/extract/html/pages_test.go`

**Interfaces:**
- Consumes: `fieldPages`, the pacer and `saveRule` events from Task 9. `applyLinks`, `parseSelector`, `vocabulary.ground`, `temperatures`, `minLinks`, `ErrAbstained`, `ErrNoRule`, `retryGeneration` and the `generating` semaphore are already in the package.
- Produces:
  - **`port.Extractor`** gains `Next(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte) (string, error)`. It returns the URL of the listing page after `url`, or `""` when there is none or no rule yet.
  - **The rule type**, with JSON keys `next`, `template` and `reasoning`:

    ```go
    type PageRule struct {
    	Next      *string `json:"next" jsonschema:"nullable"`
    	Template  *string `json:"template" jsonschema:"nullable"`
    	Reasoning string  `json:"reasoning"`
    }
    ```

    It has `JSONSchemaExtend`, using `prompts.pageFields`, plus `Abstained`, true when both are nil or blank, and `sanitize`, like `LinkRule`.
  - **`pageCandidates(listing sample, links LinkRule) []candidate`.** A candidate holds `tag`, `classes`, `rel`, `ariaLabel`, `text` and `url`.
    - It reads URLs from `href`, `data-href`, `data-url` and `data-next`.
    - It keeps only URLs on the listing's host that are not the listing itself.
    - It drops elements the `links` rule matches.
    - It dedupes by URL, truncates `text` to 40 characters and caps the list at 80.
  - **`applyPage(rule PageRule, listing sample, n int) (string, error)`.** `n` is the number of the page wanted.
    - `Next` reads the first match's URL from `href`, then `data-href`, `data-url`, `data-next`.
    - `Template` replaces `{n}` with `n`.
    - The result must be on the listing's host and differ from `listing.URL`. Otherwise it returns `""`.
  - **`checkPageRule(ctx, rule PageRule, links LinkRule, listing sample, fetch fetchFunc) error`**, as in the spec's Check:
    - page 2 gives at least `minLinks` links, at least half of them new,
    - and a `Next` rule gives a page 3 different from pages 1 and 2.
  - **`generatePageRule(ctx, l, listing sample, links LinkRule, fetch fetchFunc) (PageRule, error)`** mirrors `generateLinkRule`. Its system prompt is `prompts.pagesSystem()`. Its user prompt is `prompts.candidates(cands)`, one line per candidate: `[i] <tag class="..." rel="..." aria-label="..."> "text" -> url`.
  - **`htmlExtractor.Next`:**
    - With a `pages` rule, it returns `applyPage(rule, s, 2)`.
    - Without a `links` rule, it returns `""`.
    - Without a `pages` rule, it starts `generatePageRule` in a goroutine and returns `""`. The goroutine runs only off-peak, when `generating` is free and `fieldPages` is not failing for the domain. On `port.ErrUnavailable` it returns. On another error it calls `t.fail(fieldPages, d.ID)`. On success it calls `saveRule`.
- **`prompts/pages_system.md`** follows `links_system.md`:
  - It explains the candidate line format.
  - It asks for `next`, a selector for the element linking to the next, older page, or `template`, a URL with `{n}` for the page number. Exactly one of the two.
  - It keeps the same selector rules, with names only from the candidates.
  - Abstaining with both null is better than a guess.
- **`prompts/page_rule.md`** has sections `## next`, `## template` and `## reasoning`.

- [ ] **Step 1: Write the tests in `pages_test.go`**

| Test | Asserts |
|---|---|
| `TestPageCandidates` | A listing with an article link the rule matches, a `rel="next"` anchor, a `<button data-url="/news?page=2">Load more</button>`, an off-host link and a self link gives exactly 2 candidates, in DOM order, with the right `rel` and text. |
| `TestApplyPage` | `Next: "a.older"` gives the absolute `href`. `Next: "button.more"` gives its `data-url`. `Template: "https://a.example/news?page={n}"` with `n = 3` gives `...page=3`. A template on another host gives `""`. |
| `TestCheckPageRule` | Against an `httptest` server whose page 2 has 6 new article links, the rule passes. When page 2 repeats page 1's links, it fails. When a `Next` rule's page 3 equals page 2, it fails. |
| `TestNextGeneratesWithoutRule` | A domain with a `links` rule and no `pages` rule, and a `fakeLLM` answering a valid `PageRule`. `Next` returns `""`. After `wait(tr)` the store holds key `pages`. |
| `TestNextWithoutLinkRule` | `Next` returns `""` with no LLM call. |

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/adapter/extract/html/ -run 'TestPage|TestApplyPage|TestCheckPageRule|TestNext' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `pages.go`, the two prompt files, `prompts.pagesSystem`, `prompts.candidates`, `prompts.pageFields` and `Next`**

Also add a `Next` returning `"", nil` to `fakeExtractor` and to any other `port.Extractor` fake.

- [ ] **Step 4: Run the tests and build**

Run: `go build ./... && go test -race ./internal/adapter/extract/html/ ./internal/core/service/source/`
Expected: PASS. Set `TEST_POSTGRES_CONN` for the DB tests in `source`.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/extract/html internal/core/port/extract.go internal/core/service/source/extract_test.go
git commit -m "Learn a pagination rule per domain like the link rule"
```

### Task 11: Page backfill strategies

**Files:**
- Create: `internal/adapter/fetcher/paging.go`
- Modify: `internal/adapter/fetcher/page.go`
- Test: `internal/adapter/fetcher/paging_test.go`

**Interfaces:**
- Consumes: `wordpressArchive` from Task 4, `port.Backfiller` and `port.ErrNoArchive` from Task 4, and `Next` from Task 10.
- Produces:
  - The `Linker` interface in `page.go` gains `Next(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte) (string, error)`.
  - **The pager type:**

    ```go
    // pager gives the url of listing page n, the page after cur. "" ends the run.
    type pager func(ctx context.Context, l *slog.Logger, d domain.Domain, cur string, body []byte, n int) (string, error)
    ```

  - **`func (r page) Backfill(ctx, l, url string) (<-chan pageDocument, error)`**, with `var _ port.Backfiller[pageDocument] = page{}`:
    1. `wordpressArchive` with `logs.Client(r.client, l, "wordpress")` and `r.pacer`. When `ok`, map each `rssDocument` to `pageDocument{URL, Lang}`.
    2. Otherwise fetch page 1 through the pacer, then get its domain and its links through `r.linker.Links`.
    3. No links means no `links` rule. Return `fmt.Errorf("%w: no links on %s", port.ErrNoArchive, url)`.
    4. Pick the pager. Try `relNext`, `patterns(url)` and `r.ruled` in order, and keep the first that returns a non-empty URL for page 1.
    5. Stream from a goroutine:
       - Send the unseen links of each page.
       - Fetch the next page through the pacer.
       - Stop on a fetch error, on no unseen links, on an empty next URL, or on a URL already visited.
  - **`relNext`** reads the `href` of `link[rel=next]`, then `a[rel=next]`, resolved against `cur`.
  - **`patterns(base string) pager`** is a closure.
    - For `n = 2`, it tries `base` with `/page/2/` appended to the path, then `?page=2`, `?paged=2` and `?p=2`.
    - It keeps the first whose page 2 gives a link that page 1 did not. That check costs one paced fetch per tried pattern.
    - For later pages it reuses the chosen pattern with `n`.
  - **`r.ruled`** calls `r.linker.Next`.

- [ ] **Step 1: Write the tests in `paging_test.go`**

Use a fake `Linker` whose `Links` returns the `href`s of `a.post`, resolved, and whose `Next` returns `next[url]` from a map.

| Test | Server | Want links |
|---|---|---|
| `TestPageBackfillRelNext` | `/news` → p1, p2 and `<link rel="next" href="/news?page=2">`. `?page=2` → p3 and next `?page=3`. `?page=3` → p4, no next. | p1, p2, p3, p4 |
| `TestPageBackfillPattern` | `/news` → p1, p2. `/news/page/2/` → 404. `/news?page=2` → p3. `?page=3` → p3. | p1, p2, p3 |
| `TestPageBackfillRepeatsPageOne` | every `?page=N` answers page 1 | p1, p2, and at most 6 requests |
| `TestPageBackfillRule` | `/news` → p1, with no next and no pattern pages. Fake `Next` maps `/news` → `/older?c=x` → p5. | p1, p5 |
| `TestPageBackfillNoLinkRule` | the fake's `Links` returns nil | `errors.Is(err, port.ErrNoArchive)` |
| `TestPageBackfillWordPress` | `wordpressServer(t, "/wp-json/")` at `/blog/` | `https://x.example/a`, `/b`, `/c`, each with `Lang == "sv"` |

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/adapter/fetcher/ -run TestPageBackfill -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `paging.go` and `page.Backfill`**

- [ ] **Step 4: Run the package tests and build**

Run: `go build ./... && go test -race ./internal/adapter/fetcher/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/fetcher/paging.go internal/adapter/fetcher/paging_test.go internal/adapter/fetcher/page.go
git commit -m "Backfill page sources through WordPress, rel next, URL patterns and a pagination rule"
```

### Task 12: Rule events start page backfills

**Files:**
- Create: `internal/core/service/source/ruled.go`
- Modify: `internal/core/domain/source.go`
- Modify: `internal/adapter/postgres/source.go`
- Modify: `cmd/main.go`
- Modify: `docs/kb/backfill.md`
- Test: `internal/core/service/source/ruled_test.go`

**Interfaces:**
- Consumes: `ruled` and `RuledQueue` from Task 9, and `backfills` from Task 5.
- Produces:
  - `domain.SourceFilter` gains `DomainID sql.Null[string]` and `Adapter sql.Null[string]`. `ListSources` adds `AND domain_id = $n` and `AND adapter = $n` when they are valid.
  - **`BackfillRuled`:**

    ```go
    // BackfillRuled backfills a domain's page sources once a rule they need is saved.
    func BackfillRuled(ctx context.Context, s *postgres.SourceStore, ruled <-chan string, backfills chan<- domain.Source, l *slog.Logger)
    ```

    For each domain id, it lists that domain's sources with `Enabled` true, `Status` active and `Adapter` `page`. It sends each one on `backfills` without blocking. A full channel logs `l.Warn("backfill queue full", "source", id)`. It returns when `ctx` ends.

- [ ] **Step 1: Write `TestBackfillRuled`**

One domain with three sources: an enabled active `page`, an enabled active `rss`, and a disabled `page`.
- Send the domain id on `ruled`.
- Assert `backfills` receives only the enabled `page` source, within 2 s.
- With `backfills` full, a second domain id does not block.

- [ ] **Step 2: Run it to verify it fails**

Run: `TEST_POSTGRES_CONN=... go test ./internal/core/service/source/ -run TestBackfillRuled -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement the filter, `BackfillRuled`, and `g.Go` it in `cmd/main.go` with `ruled` and `backfills`**

- [ ] **Step 4: Run every Go test and build**

Run: `go build ./... && TEST_POSTGRES_CONN=... go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Add page backfill to `docs/kb/backfill.md`**

Cover the strategies, the pagination rule, rule events and the plain-HTTP rule.

- [ ] **Step 6: Commit**

```bash
git add internal/core/service/source/ruled.go internal/core/service/source/ruled_test.go internal/core/domain/source.go internal/adapter/postgres/source.go cmd/main.go docs/kb/backfill.md
git commit -m "Backfill a domain's page sources when its link or pagination rule is saved"
```
