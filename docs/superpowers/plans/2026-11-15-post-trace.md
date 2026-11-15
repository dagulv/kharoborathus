# Post Trace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The admin Posts page lists every raw post. A selected post shows a Content tab and a Logs tab. Logs is an accordion step graph with the input and output of every step and every external call.

**Architecture:**
- Every path claims its `raw_posts` row before the first fetch, so every later step and call can carry the raw post id.
- The article fetch moves out of the rss and page adapters into one pipeline stage, `Service.fetch`, which uses `port.PageFetcher`.
- Each step writes one `raw_post_steps` row. Each external call goes through the `calls` logger with `raw_post_id` and `step` in metadata.
- A trace endpoint joins steps and log summaries. The SPA loads call bodies lazily from the existing `GET /admin/logs/{id}`.

**Tech Stack:** Go 1.x, pgx v5, Postgres, SvelteKit 3 (next), Svelte 5 runes, Tailwind v4, shadcn-svelte.

**Spec:** `docs/superpowers/specs/2026-11-15-post-trace-design.md`. The approved mockup is `.superpowers/brainstorm/434295-1794748688/content/post-logs-v2.html`.

## Global Constraints

- Edit `internal/migrations/001_init.sql` in place. Add no new migration file.
- Go style follows `~/.claude/CLAUDE.md`:
  - `any`, not `interface{}`.
  - Leaf errors are wrapped as `fmt.Errorf("err <what>: %w", err)`.
  - Guard clauses and early returns.
  - Assign the default value first, then override it.
- Scan nullable text with `sql.NullString`, as `scanSource` does.
- Comment only non-obvious intent. Never narrate code.
- Messages go to the stdout logger `l`. Outbound requests go to the `calls` logger. Never send `l.Info`, `l.Warn` or `l.Error` through `calls`, since every record there becomes a `logs` row.
- A failed step or log write never changes a post's outcome. Log it on `l` and continue.
- Calls are linked to a post by id only. The one exception is the extract rule call, which is linked by domain and time, as the spec decides.
- Shared source-run calls stay out of post traces: feed, listing, WordPress archive, link-rule trim and link-rule deepseek.
- Never use `npx`. Use `pnpm dlx`.
- Commit after every task. Never push.
- Run Go DB tests against the local Postgres. They use `dbtest.New(t)`.

## Review Focus

1. **A step or log write fails.** The post still gets its normal status. Pinned in Task 4 by `TestStepWriteFailureKeepsOutcome`.
2. **A URL already accepted, rejected or published is seen again by a sync.** It is not fetched again. Pinned in Task 2 by `TestClaimRawPost`.
3. **The live lane and a backfill lane see the same URL at once.** Only one claims it. Pinned in Task 2 by `TestClaimRawPostOnce`.
4. **A 5 MB page fetch is on the trace.** The trace response carries no bodies, and the SPA fetches a body only when its row opens. Pinned in Task 8 by `TestTraceHasNoBodies`.
5. **A post traced before this change, on a source with other posts.** Only its own fetch call shows, never a sibling's. Pinned in Task 8 by `TestTraceLegacy`.

---

### Task 1: Schema, statuses and the step store

**Files:**
- Modify: `internal/migrations/001_init.sql`
  - `raw_posts`: `source_id` drops `NOT NULL`. `lang` gets `DEFAULT ''`. Add `discovery_post_id UUID REFERENCES discovery_posts (id)`. Status comment gets `| missing`.
  - Add the `raw_post_steps` table.
  - `discovery_posts` is created after `raw_posts`. Either move the `raw_posts` table below it, or add the FK with an `ALTER TABLE ... ADD CONSTRAINT` after both exist.
- Modify: `internal/core/domain/post.go`. Add `StatusMissing = "missing"`.
- Create: `internal/core/domain/step.go`
- Create: `internal/adapter/postgres/step.go`
- Test: `internal/adapter/postgres/step_test.go`

```sql
CREATE TABLE IF NOT EXISTS raw_post_steps (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  raw_post_id UUID NOT NULL REFERENCES raw_posts (id) ON DELETE CASCADE,
  name TEXT NOT NULL, -- found_by | domain_check | fetch | extract | date_window | source_match | duplicate_check | quality_check | enrich | discover
  outcome TEXT NOT NULL, -- passed | stopped | error
  reason TEXT NOT NULL DEFAULT '',
  input JSONB NOT NULL DEFAULT '{}',
  output JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS raw_post_steps_raw_post_id_idx ON raw_post_steps (raw_post_id, id);
```

**Interfaces:**
- Produces in `domain/step.go`:
  - Step name constants: `StepFoundBy`, `StepDomainCheck`, `StepFetch`, `StepExtract`, `StepDateWindow`, `StepSourceMatch`, `StepDuplicateCheck`, `StepQualityCheck`, `StepEnrich`, `StepDiscover`. Their values are the snake_case names in the SQL comment.
  - Outcome constants: `OutcomePassed = "passed"`, `OutcomeStopped = "stopped"`, `OutcomeError = "error"`, `OutcomePending = "pending"`. Pending is used only by the trace.
  - The `Step` struct:
    ```go
    type Step struct {
    	ID        int64          `json:"id"`
    	RawPostID string         `json:"rawPostId"`
    	Name      string         `json:"name"`
    	Outcome   string         `json:"outcome"`
    	Reason    string         `json:"reason"`
    	Input     map[string]any `json:"input"`
    	Output    map[string]any `json:"output"`
    	CreatedAt time.Time      `json:"createdAt"`
    }
    ```
- Produces in `postgres/step.go`:
  - `type StepStore struct{ db *database.Database }`
  - `func NewStepStore(db *database.Database) StepStore`
  - `func (s *StepStore) RecordStep(ctx context.Context, st domain.Step) error`. A nil Input or Output is stored as `{}`.
  - `func (s *StepStore) ListSteps(ctx context.Context, rawPostID string) ([]domain.Step, error)`, ordered by id.

- [ ] **Step 1: Write `TestRecordStep` in `step_test.go`.** Seed a domain, a source and a raw post with plain SQL inserts, following the pattern in `raw_post_test.go`. Record two steps: fetch passed, then quality_check stopped with reason `score 0.12, needs 0.80` and output `{"score":0.12}`. Assert:
  - `ListSteps` returns 2 steps in that order.
  - The second step has Outcome `stopped`, the reason above, and `Output["score"] == 0.12`.
  - The first step's Input is an empty map, not nil.
- [ ] **Step 2: Run the test and check that it fails to compile.** Run: `go test ./internal/adapter/postgres/ -run TestRecordStep`
- [ ] **Step 3:** Make the schema edit, add `StatusMissing`, add `domain/step.go`, and add `postgres/step.go`. Errors are wrapped as `err record step` and `err list steps`.
- [ ] **Step 4:** Run `go test ./internal/adapter/postgres/ -run TestRecordStep`. Expected: PASS.
- [ ] **Step 5: Commit.** Message: `Add raw post steps and the missing status`.

---

### Task 2: Claim before fetch in the raw post store

**Files:**
- Modify: `internal/adapter/postgres/raw_post.go`
- Test: `internal/adapter/postgres/raw_post_test.go`. The `create` helper now uses `ClaimRawPost` followed by `SaveContent`.

**Interfaces:**
- Produces:
  - `type Claim struct{ URL, Lang, SourceID, DiscoveryPostID string }`. An empty `SourceID` or `DiscoveryPostID` is stored as NULL.
  - `func (s *RawPostStore) ClaimRawPost(ctx context.Context, c Claim) (domain.RawPost, bool, error)`
    - It upserts the domain from `domain.BaseURL(c.URL)` in the same statement, as a CTE with `ON CONFLICT (base_url) DO UPDATE ... RETURNING id`.
    - It inserts the raw post as `pending`.
    - On a url conflict it sets `status = 'pending'` only when the existing row has `content IS NULL` and one of these holds:
      - `status IN ('failed','missing')`, or
      - `status = 'pending'` and `created_at < now() - interval '5 minutes'`.
    - It never touches `created_at`.
    - `bool` is false when no row came back.
  - `func (s *RawPostStore) SaveContent(ctx context.Context, p domain.RawPost) error` updates these columns by id:
    - `domain_id` and `lang`
    - `raw_content`
    - `content`, the same JSON shape `CreateRawPost` writes today
    - `simhash`
    - `source_id = coalesce(NULLIF($,'')::uuid, source_id)`
  - `domain.RawPost` gains `DiscoveryPostID string` with tag `json:"discoveryPostId"`, scanned with `sql.NullString`. `SourceID` is also scanned with `sql.NullString`. `rawPostColumns` gains `discovery_post_id`.
  - `domain.RawPost` gains `FinalURL string` with tag `json:"-"`. It is never stored and is set by the fetch stage.
- Keeps `CreateRawPost` and `RawPostExists` for now, so the build stays green. Task 5 deletes them once nothing calls them.
- Changes:
  - `ListPending` adds `AND content IS NOT NULL`.
  - `ListUnenriched` adds `AND r.content IS NOT NULL`.

- [ ] **Step 1: Write `TestClaimRawPost`.** It is table-driven. Each case seeds a row at a URL with a given status and content, then claims that URL again.

  | existing row | want claimed |
  | --- | --- |
  | none | true, and the new row is `pending` with a domain id |
  | `failed`, content NULL | true |
  | `missing`, content NULL | true |
  | `failed`, content set (enrich failure) | false |
  | `accepted` | false |
  | `rejected` | false |
  | `pending` created 1 minute ago | false |
  | `pending` created 10 minutes ago, content NULL | true |

  Also assert that a claim with an empty `SourceID` stores NULL, and that `GetRawPost` scans it to `""`.
- [ ] **Step 2: Write `TestClaimRawPostOnce`.** Claim the same new URL twice in a row. Assert the first returns true and the second false.
- [ ] **Step 3: Write `TestSaveContent`.** Claim, then `SaveContent` with title, content, authors, a simhash, lang `en` and a source id. Assert `GetRawPost` returns them. A second `SaveContent` with an empty `SourceID` keeps the source id.
- [ ] **Step 4: Write `TestListsSkipRowsWithoutContent`.** A pending row and a failed row, both with NULL content and older than the gap. Assert `ListPending` and `ListUnenriched` return neither.
- [ ] **Step 5:** Run `go test ./internal/adapter/postgres/ -run 'Claim|SaveContent|WithoutContent'`. Expected: FAIL.
- [ ] **Step 6:** Implement it, and update `TestHasDuplicate` and the other existing tests to the new helper.
- [ ] **Step 7:** Run `go build ./... && go test ./internal/adapter/postgres/`. Expected: PASS.
- [ ] **Step 8: Commit.** Message: `Claim raw posts before they are fetched`.

---

### Task 3: Adapters emit candidates without fetching articles

**Files:**
- Modify: `internal/adapter/fetcher/rss.go`
  - `posts` sends `item.rawPost()` for every item.
  - Delete `article` and `rssDocument.Page`.
  - `rawPost` leaves `RawContent` empty.
- Modify: `internal/adapter/fetcher/page.go`. `posts` sends `domain.RawPost{URL, Lang}` for every link. Delete the `PostStore` interface and the `store` field.
- Modify: the constructors and their tests:
  - `NewRSS(pacer *pace.Pacer, l *slog.Logger) port.Backfiller`
  - `NewPage(domains DomainStore, linker Linker, pacer *pace.Pacer, l *slog.Logger) port.Backfiller`
  - Tests to update: `rss_test.go`, `paging_test.go`, `wordpress_test.go`.
  - Update the two calls in `cmd/main.go`, so the build stays green. Articles are not fetched until Task 4, which is fine within the branch.
- Modify: `internal/core/port/fetcher.go`. The `Fetcher` doc becomes: "sends one post per item found. The pipeline claims and fetches each one."

**Interfaces:**
- Produces: adapters send RawPosts with only `URL`, `Lang` and any feed fields set: `Title`, `Content`, `Authors`, `PostedAt`.

- [ ] **Step 1:** Change `TestRSS` or the closest existing rss test so it asserts this:
  - Two feed items produce two posts.
  - Each post keeps the feed title and content.
  - `RawContent` is empty.
  - The test server receives only the feed request, with no article requests.

  Add the same assertion to the page adapter test: one listing request, and no article requests.
- [ ] **Step 2:** Run `go test ./internal/adapter/fetcher/`. Expected: FAIL, because article requests are still made.
- [ ] **Step 3:** Implement it.
- [ ] **Step 4:** Run `go build ./... && go test ./internal/adapter/fetcher/`. Expected: PASS.
- [ ] **Step 5: Commit.** Message: `Leave article fetching to the pipeline`.

---

### Task 4: Sourced pipeline records steps and calls

**Files:**
- Modify: `internal/core/service/source/source.go`
- Modify: `internal/core/service/source/sourced.go`
- Modify: `internal/core/service/source/extract.go`
- Modify: `internal/core/service/source/quality.go`
- Modify: `internal/core/service/source/retry.go`. It only picks up the new `checkQuality`.
- Create: `internal/core/service/source/step.go`
- Modify: `cmd/main.go`
- Test: `sourced_test.go`, `extract_test.go`, `quality_test.go` and `retry_test.go` in the same package

**Interfaces:**
- Consumes:
  - Task 1: `StepStore.RecordStep` and the domain constants.
  - Task 2: `ClaimRawPost`, `SaveContent` and `RawPost.FinalURL`.
  - Task 3: candidate posts.
- `Service` gains these fields:
  - `Steps StepRecorder`
  - `Pages port.PageFetcher`
  - `Calls *slog.Logger`, the logger that writes to the `logs` table
- `type StepRecorder interface{ RecordStep(ctx context.Context, st domain.Step) error }` in `step.go`, so tests can inject a failing recorder.
- `NewService(store, domains, rawPosts, steps StepRecorder, extractor, classifier, pages port.PageFetcher, enriched, schedule, resyncs, calls *slog.Logger) Service`
- In `step.go`:
  - `func (s *Service) step(ctx context.Context, l *slog.Logger, st domain.Step)` records the step and logs a failure on `l`.
  - `func (s *Service) callsFor(rawPostID, step string) *slog.Logger` returns `s.Calls.With("raw_post_id", rawPostID, "step", step)`.
  - `func (s *Service) stop(ctx context.Context, l *slog.Logger, p domain.RawPost, status string, st domain.Step) error` calls `SetStatus`, records `st` with Outcome `stopped`, and returns `pipeline.ErrSkip`. Two exceptions:
    - It returns the `SetStatus` error when that fails.
    - It records the step even then.
- Pipeline order in `Run`: `claim`, `fetch`, `Extract`, `SetMarkdown`, `SetSimhash`, `saveContent`, `checkQuality`, then the `saveStatus` sink. `insert` stays until Task 5, since the discovered path still calls it.
- `func (s *Service) claim(ctx context.Context, l *slog.Logger, p domain.RawPost) (domain.RawPost, error)`
  - It claims `postgres.Claim{URL: p.URL, Lang: p.Lang, SourceID: p.SourceID}`.
  - It returns `ErrSkip` when nothing is claimed.
  - It copies ID, DomainID and CreatedAt from the claimed row and keeps the feed fields.
- `func (s *Service) fetch(ctx context.Context, l *slog.Logger, p domain.RawPost) (domain.RawPost, error)`
  - It calls `s.Pages.Page(ctx, s.callsFor(p.ID, domain.StepFetch), p.URL)` and sets `RawContent` and `FinalURL`.
  - Step input is `{"url": p.URL}`. Output is `{"bytes": n, "finalUrl": u, "feedChars": len(p.Content)}`.
  - An error with empty feed content stops the post as `failed`, with reason `fetch failed: <err>`.
  - An error with feed content passes, with reason `article fetch failed, kept feed content: <err>`.
  - Shutdown returns `ctx.Err()` and records nothing.
- `Extract`:
  - It resolves the domain and extracts from `cmp.Or(p.FinalURL, p.URL)`.
  - It passes `s.Calls.With("domain_id", d.ID)` to the `Extractor` methods, and keeps `l` for its own Debug and Error lines. So `extract` gains a `calls *slog.Logger` parameter.
  - On empty content it stops as `missing` with reason `no content found`.
  - On success it records a passed step:
    - Input: `{"bytes": n, "rules": sorted keys of d.Metadata.Extract}`
    - Output: `{"title", "authors", "postedAt", "contentChars"}`
- `func (s *Service) saveContent(ctx context.Context, l *slog.Logger, p domain.RawPost) (domain.RawPost, error)` calls `SaveContent`.
- `checkQuality` records its steps as follows:
  - **duplicate_check:** input `{"simhash": p.Simhash}`, output `{"duplicate": bool}`.
    - A duplicate gets Outcome `stopped`, reason `near duplicate of an earlier post`, and status `duplicate`.
    - Otherwise it passes.
  - **quality_check** runs `isArticle(ctx, c, calls, title, markdown) (float64, error)` with `calls = s.callsFor(p.ID, domain.StepQualityCheck)`.
    - Output is `{"score": s, "threshold": articleThreshold}`.
    - Below the threshold: stopped, reason `fmt.Sprintf("score %.2f, needs %.2f", score, articleThreshold)`.
    - A Jev error: Outcome `error`, reason `err.Error()`, and today's `ErrSkip` return so the post stays pending.
    - A nil classifier: passed, reason `quality check off`.
  - `checkQuality` keeps today's contract of returning the post with its status set. The sink saves the status, as today.
- `cmd/main.go`:
  - Build `stepStore := postgres.NewStepStore(db)` and pass `&stepStore`, `fetcher.NewPages(pacer)` and `calls` to `NewService`.

- [ ] **Step 1:** Write `TestFetchFailedStopsPost` and `TestFetchFailedKeepsFeedContent` in `sourced_test.go`. Use a fake `PageFetcher` that returns an error, and a DB-backed service from `dbtest`.
  - The first sends a post with no feed content. Assert raw status `failed` and one fetch step, stopped, with a reason starting `fetch failed:`.
  - The second has feed content. Assert the fetch step passed with a reason starting `article fetch failed`, and the post reaches `checkQuality`.
- [ ] **Step 2:** Write `TestExtractMissing` in `extract_test.go`. Empty page and empty feed content give raw status `missing` and an extract step stopped with reason `no content found`.
- [ ] **Step 3:** Write `TestQualitySteps` in `quality_test.go`. It is table-driven with the existing `fakeClassifier`:

  | case | status | quality step |
  | --- | --- | --- |
  | score 0.12 | rejected | stopped, `score 0.12, needs 0.80` |
  | score 0.9 | accepted | passed |
  | err `port.ErrUnavailable` | pending | error |
  | nil classifier | accepted | passed, `quality check off` |

  Also assert there is a duplicate_check step before the quality_check step. Add a duplicate case: duplicate_check stopped, status `duplicate`, and no quality_check step.
- [ ] **Step 4:** Write `TestQualityCallsCarryRawPostID`. A fake classifier records the `*slog.Logger` it gets. Call it through a `slog.New` handler that captures attrs, and assert `raw_post_id` equals the post id and `step` equals `quality_check`.
- [ ] **Step 5:** Write `TestStepWriteFailureKeepsOutcome`. Use a `StepRecorder` fake whose `RecordStep` always errors. A score of 0.9 still gives status `accepted`, and the error is logged on `l`.
- [ ] **Step 6:** Run `go test ./internal/core/service/source/`. Expected: FAIL.
- [ ] **Step 7:** Implement it, then update `TestLane`, `retry_test.go` and the other tests to the new constructor and stages.
- [ ] **Step 8:** Run `go build ./... && go test ./internal/core/service/source/ ./internal/adapter/...`. Expected: PASS. Discovered still uses `insert` and its old flow until Task 5.
- [ ] **Step 9: Commit.** Message: `Record each sourced pipeline step and its calls`.

---

### Task 5: Discovered path claims first and records steps

**Files:**
- Modify: `internal/core/service/source/discovered.go`
- Modify: `internal/adapter/postgres/discovered.go`. The `Discovered` struct gains `QueryID string`, read from `discovery_search_query_id` with `sql.NullString`.
- Modify: `cmd/main.go`. Drop `Pages` and `Calls` from `source.Discovered`, since `Service` owns both now.
- Delete: `Service.insert`, `RawPostStore.CreateRawPost` and `RawPostStore.RawPostExists`, which have no callers left.
- Test: `internal/core/service/source/discovered_test.go`

**Interfaces:**
- Consumes: the Task 4 `Service` stages and helpers.
- `Discovered` keeps `Service` and `Store`.
- `ingest` order, where every stop uses `s.stop`:
  1. **claim** with `postgres.Claim{URL: link.URL, DiscoveryPostID: link.ID}`. If nothing is claimed, set the discovery status to `duplicate` and return.
  2. **found_by** passes. Input is `{"discoveryPostId", "fromPostId", "searchQueryId"}`.
  3. **domain_check:** a blocked domain stops as `failed` with reason `domain blocked`. It runs before the fetch, as today.
  4. **fetch** is the shared `Service.fetch`. Lang is set from `normalizeLang(htmlLang(page))` before extraction.
  5. **extract** is the shared `Service.Extract`. It stops as `missing`, not `rejected` as today.
  6. **date_window:** a `PostedAt` older than `discoveredWindow` days stops as `rejected`, with reason `posted 2006-01-02, older than 7 days`.
  7. `SetMarkdown` and `SetSimhash`.
  8. **source_match** checks `DomainSource(p.DomainID)`.
     - A blocked source stops as `failed` with reason `source blocked`.
     - With no source, it creates a proposed one, as today.
     - Output is `{"sourceId", "proposed": bool}`. Lang falls back to the source's lang.
  9. `saveContent`, then `checkQuality` and `saveStatus`, as today.
- The discovery status mirrors the raw status, except `missing`, which maps to `rejected`. A raw post that reaches the quality check marks the discovery `accepted`, as today.

- [ ] **Step 1:** Rewrite the cases in `discovered_test.go` around these assertions. Each checks the raw status, the discovery status and the ordered step names with their outcomes:

  | case | raw status | discovery status | steps |
  | --- | --- | --- | --- |
  | url already a raw post | none created | duplicate | none |
  | blocked domain | failed | failed | found_by, domain_check stopped |
  | fetch error | failed | failed | found_by, domain_check, fetch stopped |
  | empty page | missing | rejected | ends extract stopped |
  | posted 30 days ago | rejected | rejected | ends date_window stopped |
  | happy path, score 0.9 | accepted | accepted | found_by, domain_check, fetch, extract, date_window, source_match, duplicate_check, quality_check |

  The happy path also asserts that `raw_posts.discovery_post_id` equals the link id.
- [ ] **Step 2:** Run `go test ./internal/core/service/source/ -run Discovered`. Expected: FAIL.
- [ ] **Step 3:** Implement it.
- [ ] **Step 4:** Run `go build ./... && go test ./internal/...`. Expected: PASS.
- [ ] **Step 5: Commit.** Message: `Record each discovered ingest step`.

---

### Task 6: Enrich logs its calls and records enrich and discover steps

**Files:**
- Modify: `internal/core/service/enrich/enrich.go`
- Modify: `cmd/main.go`
- Test: `internal/core/service/enrich/enrich_test.go`

**Interfaces:**
- `enrich.Service` gains `Steps StepRecorder` and `Calls *slog.Logger`. `StepRecorder` is a local interface with the same `RecordStep` method, so enrich does not import the source service.
- `func (s *Service) enrich(ctx context.Context, l *slog.Logger, rawPostID string) (domain.Post, discovery.Found, error)`. The post is the inserted one, and its `ID` is `""` when enrich skipped.
- Inside `enrich`, `calls := s.Calls.With("raw_post_id", p.ID, "step", domain.StepEnrich)` goes to `jevTags` and `s.LLM.Unmarshal`. `l` stays for messages.
- `Enrich` records steps:
  - On an error that is not `ErrPeakHours` or shutdown: an enrich step with Outcome `error` and reason `err.Error()`. This happens on every attempt.
  - When a post was inserted:
    - an enrich step, passed, output `{"postId", "tags", "summary"}`
    - then a discover step, passed, output `{"queries": found.Queries, "links": len(found.Links)}`
  - A skip records nothing.

- [ ] **Step 1:** Write `TestEnrichSteps`, extending the existing enrich test setup.
  - A successful enrich gives steps `enrich` passed with the post id, then `discover` passed.
  - A failing LLM gives one `enrich` error step, and a second `Enrich` call adds a second error step.
  - A post that already exists gives no steps.
- [ ] **Step 2:** Write `TestEnrichCallsCarryRawPostID`. The LLM and classifier fakes capture the logger and assert `raw_post_id` and `step=enrich`.
- [ ] **Step 3:** Run `go test ./internal/core/service/enrich/`. Expected: FAIL.
- [ ] **Step 4:** Implement it, and pass `&stepStore` and `calls` in `cmd/main.go`.
- [ ] **Step 5:** Run `go build ./... && go test ./internal/core/service/enrich/`. Expected: PASS.
- [ ] **Step 6: Commit.** Message: `Log enrich calls and record enrich and discover steps`.

---

### Task 7: Admin lists raw posts

**Files:**
- Modify: `internal/core/domain/admin.go`
- Modify: `internal/adapter/postgres/post.go`. `ListAdminPosts` and `GetAdminPost` are rewritten over `raw_posts`.
- Modify: `internal/core/service/admin/posts.go`
- Test: `internal/adapter/postgres/post_test.go` or an admin post store test, plus `internal/core/service/admin/posts_test.go`

**Interfaces:**
- `domain.AdminPostFilter` gains `Status string`. Valid values are `""`, `published`, `accepted`, `pending`, `rejected`, `duplicate`, `missing` and `failed`. The handler rejects anything else with 400.
- These replace `domain.PostPage` and `domain.AdminPost`, which have no other users:
  ```go
  type AdminPost struct {
  	ID           string     `json:"id"` // raw post id
  	PostID       *string    `json:"postId"`
  	SourceID     *string    `json:"sourceId"`
  	DomainURL    string     `json:"domainUrl"`
  	URL          string     `json:"url"`
  	Lang         string     `json:"lang"`
  	Title        string     `json:"title"`
  	ShortContent string     `json:"shortContent"`
  	Tags         []string   `json:"tags"`
  	Authors      []string   `json:"authors"`
  	PostedAt     *time.Time `json:"postedAt"`
  	Status       string     `json:"status"` // published when a post with the same url exists
  	CreatedAt    time.Time  `json:"createdAt"`
  	Content      string     `json:"content,omitempty"`
  	SourceURL    string     `json:"sourceUrl,omitempty"`
  }
  type AdminPostPage struct {
  	Posts    []AdminPost `json:"posts"`
  	Total    int         `json:"total"`
  	Page     int         `json:"page"`
  	PageSize int         `json:"pageSize"`
  }
  ```
- `func (s *PostStore) ListAdminPosts(ctx context.Context, f domain.AdminPostFilter) (domain.AdminPostPage, error)`
  - FROM `raw_posts r JOIN domains d LEFT JOIN posts p ON p.url = r.url`.
  - Field fallbacks:
    - title: post, then `content->>'title'`, then `''`
    - posted_at: post, then `content->>'posted_at'`
    - authors: post, then `content->'authors'` when it is a JSON array, then `'{}'`
    - tags and short_content: from the post, or empty
  - Status is computed as `published` when the post exists, otherwise `r.status`.
  - Filters:
    - source matches `r.source_id`
    - tag is today's clause on `p.tags`
    - search is today's post clause, OR `r.url ILIKE`, OR `r.content->>'title' ILIKE`, each with `'%' || $n || '%'`
    - status matches the computed status
  - Order is `r.created_at DESC, r.id DESC`. Page size stays 50.
- `func (s *PostStore) GetAdminPost(ctx context.Context, rawPostID string) (domain.AdminPost, error)`
  - Same fields, plus content (`p.content`, then `content->>'content'`, then `''`) and `SourceURL` from a LEFT JOIN on sources.
  - A missing row or a non-uuid id returns `pgx.ErrNoRows`, as today.

- [ ] **Step 1:** Write `TestListAdminPostsRaw`. Seed a published raw post with a matching post, a rejected one, a missing one with NULL content, and a failed one with NULL content and NULL source. Assert:
  - No filter returns all 4, newest first.
  - The published row has `Status == "published"`, the post's title and tags, and a `PostID`.
  - The missing row has an empty title and its URL.
  - `Status: "rejected"` returns 1.
  - Searching part of the rejected post's title returns it.
  - A tag filter returns only the published row.
  - Total matches the count.
- [ ] **Step 2:** Write `TestGetAdminPostRaw`. Get by raw post id: a rejected post returns `content->>'content'`, and a NULL-source post returns an empty `SourceURL`. A random uuid gives `pgx.ErrNoRows`.
- [ ] **Step 3:** Extend `TestPostRoutes` with `?status=rejected` giving 200 and `?status=nope` giving 400. The empty list JSON keeps `posts`, `total`, `page` and `pageSize`.
- [ ] **Step 4:** Run `go test ./internal/adapter/postgres/ ./internal/core/service/admin/`. Expected: FAIL.
- [ ] **Step 5:** Implement it.
- [ ] **Step 6:** Run the same command. Expected: PASS.
- [ ] **Step 7: Commit.** Message: `List raw posts in the admin posts API`.

---

### Task 8: Trace endpoint

**Files:**
- Modify: `internal/core/domain/step.go`. Add the trace types and `BuildTrace`.
- Create: `internal/adapter/postgres/trace.go`
- Modify: `internal/core/service/admin/route.go` and `posts.go`
- Modify: `cmd/main.go`. Add `Traces` to `admin.Stores`.
- Test: `internal/core/domain/step_test.go`, `internal/adapter/postgres/trace_test.go`, `internal/core/service/admin/posts_test.go`

**Interfaces:**
- In `domain/step.go`:
  ```go
  type TraceStep struct {
  	Step
  	Calls []LogSummary `json:"calls"`
  	// The domain-level call that made the extract rule this step used. Extract only.
  	RuleCall *LogSummary `json:"ruleCall,omitempty"`
  }
  type Trace struct {
  	Path  string      `json:"path"` // sourced | discovered
  	Steps []TraceStep `json:"steps"`
  }
  ```
- `func BuildTrace(steps []Step, calls []LogSummary, byStep map[string][]LogSummary) []TraceStep`
  - `steps` are in id order. `calls` are in id order, each with `Metadata["step"]`.
  - Each call joins the first step with the same name whose `CreatedAt` is not before the call's. This is attempt matching within one post.
  - A call with no such step joins a trailing step with that name and Outcome `pending`, created once per name.
  - `byStep[name]` goes onto the last step of that name. If the post has no such step, it is dropped.
  - `Calls` is never nil.
- `type TraceStore struct{ db *database.Database }`, `func NewTraceStore(db *database.Database) TraceStore`
- `func (s *TraceStore) Trace(ctx context.Context, rawPostID string) (domain.Trace, error)`
  - A missing row or a non-uuid id returns `pgx.ErrNoRows`.
  - Path is `discovered` when `discovery_post_id` is set.
  - Steps come from `raw_post_steps`.
  - Calls are `logs WHERE metadata @> jsonb_build_object('raw_post_id', $1::text)`, selecting only `logSummaryColumns`.
  - `byStep[found_by]` holds the logs whose `metadata @>` matches the discovery post's `discovery_search_query_id`.
  - `byStep[discover]` holds the logs for each query id in `discovery_search_query_posts` joined to the post with the same url. Use one `@>` query per id, since the GIN index serves only `@>`.
  - `RuleCall` on the last extract step is the latest `type = 'deepseek'` log whose `metadata @>` matches `{"domain_id": r.domain_id}`, with `created_at <=` that step's `created_at`.
  - **Legacy:** with no steps and a non-null source, it returns one step: Name `fetch`, Outcome `passed`, reason `recorded before step tracing`. Its calls are the logs matching `metadata @> {"source_id"}` with `request->>'url' = r.url`.
- Route: `GET /admin/posts/{id}/trace` returns `domain.Trace`, or 404 `Post not found`.

- [ ] **Step 1:** Write `TestBuildTrace` in `domain/step_test.go`. It is table-driven over hand-built times:
  - Two quality_check attempts at t2 and t4, with calls at t1 and t3. The t1 call joins the first attempt and the t3 call the second.
  - A call at t5 for `enrich` with no step gives a trailing pending `enrich` step.
  - `byStep["discover"]` with no discover step is dropped.
  - A step with no calls has `Calls` as an empty slice, not nil.
- [ ] **Step 2:** Write `TestTrace` in `trace_test.go`. Seed a raw post with fetch, extract and quality_check steps.
  - Insert log rows with `metadata` set to `{"raw_post_id": id, "step": "fetch"}` and `{"raw_post_id": id, "step": "quality_check"}`.
  - Insert a log for **another** raw post id.
  - Insert a `deepseek` log with `{"domain_id": d}` from before the extract step.

  Assert:
  - 3 steps, each with its one call.
  - The other post's log is absent.
  - Extract's `RuleCall` is the deepseek log.
- [ ] **Step 3:** Write `TestTraceLegacy`. Two raw posts on the same source with no steps, and two `page` logs with `{"source_id": s}` and `request.url` set to each URL. Each trace returns one legacy fetch step that holds only its own URL's log.
- [ ] **Step 4:** Write `TestTraceHasNoBodies` in `admin/posts_test.go`. Seed a call whose response is a large string and fetch `/admin/posts/{id}/trace`. Assert the JSON has no `request` or `response` keys under `steps[].calls[]`, and that `/admin/posts/x/trace` returns 404.
- [ ] **Step 5:** Run `go test ./internal/core/domain/ ./internal/adapter/postgres/ ./internal/core/service/admin/`. Expected: FAIL.
- [ ] **Step 6:** Implement it.
- [ ] **Step 7:** Run the same command, then `go build ./...`. Expected: PASS.
- [ ] **Step 8: Commit.** Message: `Serve a step trace for each raw post`.

---

### Task 9: Admin SPA list, tabs and step graph

**Files:**
- Modify: `admin/src/lib/types.ts`:
  - `PostSummary` and `Post` mirror `domain.AdminPost`.
  - `PostPage` stays as is.
  - Add `PostStatus`, `POST_STATUSES`, `Step`, `TraceStep` and `Trace`.
- Create: `admin/src/lib/components/ui/tabs/`. Install it in `admin/` with `pnpm dlx shadcn-svelte@latest add tabs`.
- Move: `admin/src/routes/(app)/posts/+page.ts` becomes `posts/+layout.ts`. It reads `source`, `tag`, `search`, `status` and `page`.
- Create: `admin/src/routes/(app)/posts/+layout.svelte`
  - Two columns: a list about 380px wide on the left, `{@render children()}` on the right. It stacks below `md`.
  - The search form, filter badges and pagination move here from today's `+page.svelte`.
  - Status chips above the list: All, Published, Rejected, Duplicate, Missing, Failed, Pending. They link via `withParams` to `status`.
  - Each row shows the title, or the URL when there is no title, a status badge, domain, lang and time.
  - Rows link to `/posts/{id}` with the current search params kept. The selected row is highlighted.
- Modify: `admin/src/routes/(app)/posts/+page.svelte` to show a muted "Select a post" panel.
- Modify: `admin/src/routes/(app)/posts/[id]/+page.ts` to load the post and `/admin/posts/{id}/trace` in parallel.
- Modify: `admin/src/routes/(app)/posts/[id]/+page.svelte`:
  - The header shows the status badge, the title or URL, the URL, the source and the posted date.
  - Tabs `content` and `logs`, bound to `?tab=`. The default is `content`.
  - The Content tab is today's `dl`, summary and markdown `pre`. Tags show only when there are any.
- Create: `admin/src/lib/components/trace/step-graph.svelte`, `step-row.svelte` and `call-io.svelte`
- Create: `admin/src/lib/trace.ts`, with the step labels, the expected order and the dot state function

**Interfaces:**
- Consumes: the Task 7 list and get JSON, the Task 8 `Trace`, and the existing `GET /admin/logs/{id}` returning `Log`.
- `trace.ts` exports:
  - `STEP_LABELS: Record<string, string>`:
    - found_by: Found by
    - domain_check: Domain check
    - fetch: Fetch
    - extract: Extract
    - date_window: Date window
    - source_match: Source match
    - duplicate_check: Duplicate check
    - quality_check: Quality check
    - enrich: Enrich
    - discover: Discover
  - `STEP_ORDER: Record<'sourced' | 'discovered', string[]>`:
    - sourced: `fetch, extract, duplicate_check, quality_check, enrich, discover`
    - discovered: `found_by, domain_check, fetch, extract, date_window, source_match, duplicate_check, quality_check, enrich, discover`
  - `graphRows(trace: Trace, status: PostStatus): GraphRow[]`
    - It returns the recorded steps in order.
    - Then come the expected steps after the last recorded one: `{ kind: 'not-reached' }` when status is rejected, duplicate, missing or failed, and `{ kind: 'waiting' }` when it is pending or accepted. Nothing follows when it is published.
- `step-row.svelte` is a native `<details>` styled after the mockup:
  - **Dot:** green when passed with calls, grey when passed with none, red for stopped or error, amber for pending, dashed and unopenable for not reached or waiting.
  - **Summary:** chevron, label, one badge per call type, the reason (red when stopped or error), and on the right either "METHOD status · duration" for one call or "N calls".
  - **Open on load** when the outcome is stopped or error.
  - **Body:** first the step's own Input and Output as formatted JSON, when they are not empty. Then for each call:
    - a header with type, method, URL, status and duration, and a link to `/logs/{id}`
    - `call-io.svelte`
  - An extract step with `ruleCall` shows it last, labelled "Rule generation (domain)".
- `call-io.svelte` fetches `/admin/logs/{id}` once, the first time its row opens.
  - It shows request and response side by side as formatted JSON, or as raw text when the body is not JSON.
  - It stacks below `md`.
  - It shows a loading state, and an error state with the message.

- [ ] **Step 1:** Install tabs and update the types and routes, then run `pnpm -C admin check`. Expected: 0 errors.
- [ ] **Step 2:** Build `trace.ts` and the three components, then run `pnpm -C admin check && pnpm -C admin lint`. Expected: 0 errors.
- [ ] **Step 3:** Seed the smoke DB with one rejected post with steps and calls, one published post, and one missing post. Start `api-smoke` and `admin` with `preview_start` and log in, then check:
  - The status chips filter the list.
  - Selecting the rejected post opens Logs on `?tab=logs` with Quality check red, open, and showing `score 0.12, needs 0.80`.
  - Opening Fetch loads the request and response once, which shows as one network request per log id.
  - Enrich and Discover show as not reached.
  - The missing post shows its URL as the title, and Extract is red.
  - There are no console errors.
  - Take a screenshot.
- [ ] **Step 4: Commit.** Message: `Show raw posts with content and step logs in the admin`.
