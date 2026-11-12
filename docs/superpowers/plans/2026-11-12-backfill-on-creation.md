# Backfill on Source Creation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Backfill a source's WordPress archive when it is created, approved or a button is pressed. Fetch every post's article page, and keep the fetch path free of DeepSeek, Jev and Brave calls.

**Architecture:**
- **Backfill trigger.** An admin endpoint sends the source on a Go channel. A backfill goroutine runs `FindWordPress` with the shared pacer. When it finds an archive, it sends a copy with adapter `wordpress` straight into the `wordpress` plugin, which no longer sits behind `route`.
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
- **Backfill copy keeps the source's `ID`.** Posts must land on the real source. Tested in Task 4.
- **Full backfill queue.** The endpoint answers `503`, and `createSource` still redirects. Tested in Task 5 for Go; checked by hand for the form in Task 6.
- **Post with no posted date.** It counts as new, so it can still start a search. Tested in Task 3.
- **Feed content heavy in markup but short in text.** It must not beat a longer page article. Tested in Task 2.

---

### Task 1: RSS always fetches the article page

**Files:**
- Modify: `internal/adapter/fetcher/rss.go`
- Test: `internal/adapter/fetcher/rss_test.go`

**Interfaces:**
- Produces: `rss.FetchPost(ctx, l, data rssDocument) (rssDocument, error)` fetches every item's page into `data.Page`. A failed page fetch logs at debug and returns `data, nil`, keeping the feed fields. It returns an error only from `pacer.Wait`. `wordpress` embeds `rss` and inherits this.

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
Expected: PASS, including `TestWordPressFetchesEveryPage`.

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

### Task 4: Backfill stream

**Files:**
- Create: `internal/core/service/source/backfill.go`
- Test: `internal/core/service/source/backfill_test.go`

**Interfaces:**
- Consumes: `fetchers.FindWordPress(ctx, client *http.Client, pacer *pace.Pacer, rawURL string) (fetchers.WordPress, error)`. Here `fetchers` is the `internal/adapter/fetcher` import alias, as in `route.go`.
- Produces:

```go
const BackfillQueue = 16

type Backfill struct {
	Client *http.Client
	Pacer  *pace.Pacer
	// Third-party requests, one row each in the logs table.
	Calls *slog.Logger
}

// Stream sends a wordpress copy of each source whose site has a REST API. It closes when sources closes or ctx ends.
func (b Backfill) Stream(ctx context.Context, l *slog.Logger, sources <-chan domain.Source) <-chan domain.Source
```

Behaviour:
- `FindWordPress` uses `logs.Client(b.Client, b.Calls.With("source_id", source.ID), "wordpress")` and `b.Pacer`.
- An error logs `l.Warn("find wordpress", ...)` and skips the source.
- An empty `wp.Posts` logs at info and skips it.
- Otherwise it logs `l.Info("backfill", "source", id, "url", wp.Posts, "posts", wp.Total)` and sends a copy with `Adapter = "wordpress"` and `URL = wp.Posts`. `Lang` becomes `normalizeLang(wp.Lang)` only when that is not empty. Every other field, `ID` included, is unchanged.
- The output channel is unbuffered. Sends select on `ctx.Done()`.

- [ ] **Step 1: Write `TestBackfillStream`**

An `httptest` server with three handlers:
- `/` serves `<html lang="sv">` with a `<link rel="https://api.w.org/" href="/wp-json/">`.
- `/feed` serves an RSS body with the header `Link: <SRV/wp-json/>; rel="https://api.w.org/"`.
- `/wp-json/wp/v2/posts` sets `X-WP-Total: 3` and serves `[]`.
- `/plain` serves HTML with no link.

Send three sources, then close the input:

| Source | URL | Lang |
|---|---|---|
| `{ID: "a", Adapter: "page"}` | `srv.URL+"/"` | |
| `{ID: "b", Adapter: "rss"}` | `srv.URL+"/feed"` | `"en"` |
| `{ID: "c", Adapter: "page"}` | `srv.URL+"/plain"` | |

Use `pace.New(time.Millisecond)`. Collect the output until it closes, then assert:
- There are exactly 2 copies, with IDs `a` and `b`.
- Both have `Adapter == "wordpress"` and `URL == srv.URL+"/wp-json/wp/v2/posts"`.
- `a.Lang == "sv"`, and `b.Lang == "en"`, since the feed has no `<language>`.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/core/service/source/ -run TestBackfillStream -v`
Expected: FAIL to compile, since `Backfill` is undefined.

- [ ] **Step 3: Implement `backfill.go`**

- [ ] **Step 4: Run the test**

Run: `go test ./internal/core/service/source/ -run TestBackfillStream -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/service/source/backfill.go internal/core/service/source/backfill_test.go
git commit -m "Stream a wordpress copy of each source with a REST API"
```

### Task 5: Endpoint and wiring

**Files:**
- Modify: `internal/core/service/source/route.go`
- Modify: `internal/core/service/source/sourced.go`
- Modify: `cmd/main.go`
- Modify: `docs/kb/backfill.md`
- Test: `internal/core/service/source/route_test.go`
- Test: `internal/core/service/source/fetch_test.go`

**Interfaces:**
- Consumes: `Backfill`, `BackfillQueue` and `Backfill.Stream` from Task 4.
- Produces:
  - `func Routes(mux *http.ServeMux, s *postgres.SourceStore, syncs, backfills chan<- domain.Source, l, calls *slog.Logger)` registers `POST /admin/sources/{id}/backfill`.
  - `syncSource` becomes `queueSource(s *postgres.SourceStore, queue chan<- domain.Source, name string, l *slog.Logger) http.HandlerFunc`. Both routes use it, with `name` set to `"sync"` or `"backfill"`. The full-queue body reads `name + " queue full"`. Status codes are unchanged: `404`, `409`, `202`, `503`.
  - `func Run(ctx context.Context, s *Service, fetchers map[string]Fetcher, backfill Fetcher, backfills <-chan domain.Source, l *slog.Logger) error` adds `backfill.Plugin(ctx).Stream(ctx, l.With("adapter", "wordpress"), backfills)` to the merged posts. `route` never sees `backfills`.

- [ ] **Step 1: Write the tests**
  - Turn `TestSyncSource` into `TestQueueSource`. It loops over `"sync"` and `"backfill"`, keeps every existing assertion, and adds `rec.Body` containing `name + " queue full"` on the `503`.
  - Add `{http.MethodPost, "/admin/sources/nope/backfill", http.StatusNotFound}` to `TestRoutesUnderAdmin`, and pass a second channel to `Routes`.
  - Add `TestRunFeedsBackfillPlugin` in `fetch_test.go`:
    - Use `qualityServiceDB(t, nil)`. Its source is disabled, so `sourceStream` sends nothing.
    - Use a fake `Fetcher` whose `FetchFeed` sends the URL on a `fetched chan string` and returns a closed channel.
    - Call `Run(ctx, s, map[string]Fetcher{}, NewFetcher(fake, discard), backfills, discard)` in a goroutine.
    - Send `domain.Source{ID: "x", Adapter: "wordpress", URL: "https://a.example/wp-json/wp/v2/posts"}` on `backfills`.
    - Assert `fetched` receives that URL within 2 s, then cancel `ctx`.

- [ ] **Step 2: Run them to verify they fail**

Run: `TEST_POSTGRES_CONN=... go test ./internal/core/service/source/ -run 'TestQueueSource|TestRoutesUnderAdmin|TestRunFeedsBackfillPlugin' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `queueSource`, the new `Routes` and the new `Run`**

- [ ] **Step 4: Wire `cmd/main.go`**
  - Remove `"wordpress"` from the `fetchers` map.
  - Add `backfills := make(chan domain.Source, source.BackfillQueue)` next to `syncs`, and pass it to `source.Routes`.
  - Build `backfill := source.Backfill{Client: safehttp.NewClient(20 * time.Second), Pacer: pacer, Calls: calls}`.
  - In the `source.Run` goroutine, pass `source.NewFetcher(fetcher.NewWordPress(&rawPostStore, pacer, l), calls)` and `backfill.Stream(ctx, l.WithGroup("backfill"), backfills)`.

- [ ] **Step 5: Run every Go test and build**

Run: `go build ./... && TEST_POSTGRES_CONN=... go test ./...`
Expected: PASS.

- [ ] **Step 6: Rewrite `docs/kb/backfill.md`**

Match the spec:
- **Trigger:** creation, approval of a proposed source, and the button.
- **No state.** A rerun skips stored posts.
- **Paths:** `internal/core/service/source/backfill.go` and `internal/adapter/fetcher/wordpress.go`.
- **Removed:** `backfilled_at`, the batch of 10 and `RunSummaries`.

- [ ] **Step 7: Commit**

```bash
git add internal/core/service/source/route.go internal/core/service/source/sourced.go internal/core/service/source/route_test.go internal/core/service/source/fetch_test.go cmd/main.go docs/kb/backfill.md
git commit -m "Backfill a source through an admin endpoint into the wordpress plugin"
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
  - Title: `Read the site's WordPress archive, skipping stored posts`.

Use the `svelte:svelte-file-editor` agent or the Svelte MCP autofixer on the `.svelte` file.

- [ ] **Step 4: Verify**

Run: `cd frontend && pnpm check && pnpm lint`
Expected: no errors.

Check by hand against a running stack:
1. Add a WordPress source. The Go logs show `backfill` with its post count.
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
