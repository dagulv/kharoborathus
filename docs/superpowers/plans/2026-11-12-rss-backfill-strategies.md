# RSS Backfill Strategies Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Backfill an rss source's history through adapter strategies, starting with WordPress, when the source is created, approved or a button is pressed. Fetch every post's article page, and keep the fetch path free of DeepSeek, Jev and Brave calls.

**Architecture:**
- **Backfill as a strategy.** An adapter that can read history implements `port.Backfiller`. `rss` implements it with an ordered list of strategies; WordPress, found through `FindWordPress` with the rss adapter's pacer, is the first. The `wordpress` adapter type goes away.
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
- **Backfill for a `page` source.** It logs and drops the source, and never blocks. Tested in Task 5.
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
  - `func (r rss) wordpress(ctx, l, url string) (<-chan rssDocument, bool, error)`:
    - It calls `FindWordPress(ctx, logs.Client(r.client, l, "wordpress"), r.pacer, url)`.
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
