# Extractor Fields Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the trim step and the three extract interfaces with one `port.Extractor` that has a method and its own stored rule per field.

**Architecture:** The html adapter owns rules, sample pools, generation and checks. Core calls one method per field and never sees a rule. Rules are stored per field under `domains.metadata.extract`.

**Tech Stack:** Go, cascadia, golang.org/x/net/html, pgx.

**Spec:** `docs/superpowers/specs/2026-11-02-extractor-fields-design.md`

## Global Constraints

- Metadata keys are exactly `links`, `content`, `title`, `authors`, `postedAt`, under JSON key `extract`.
- A failed generation waits 24h. Article pools hold 8 pages, 3 shown and 5 held out. Link pools hold 3 listings, 30 minutes apart. At most 200 domains are sampled.
- A field rule needs a value on 3 of 5 held-out pages. Title is 5 to 300 characters. Authors are at most 10, each 2 to 100 characters. postedAt is not in the future.
- `adapter/robot` does not build in rework2. Run every go command with `-overlay $OVERLAY`. That overlay swaps the robot package for a stub holding `ModelFlash`.
- Rework2 is uncommitted work in progress. Do not commit code. Every task ends with tests passing instead.
- Go style follows `~/.claude/CLAUDE.md`: wrap leaf errors, use guard clauses, and comment only what the code can't say.

## Review Focus

- A page where the title rule matches nothing must still get a title from `h1` or `og:title`. Pinned in Task 2.
- Saving the authors rule must not remove a stored content rule. Pinned in Task 1 by the SQL shape, which uses a merge and never a replace.
- Four field methods on one page add one sample, not four. Pinned in Task 3.
- A content-only caller like RSS must not trigger authors generation. Pinned in Task 3.
- A post with fetched authors keeps them when `Authors` would find others. Pinned in Task 5.

---

### Task 1: Per-field storage

**Files:**
- Modify: `internal/core/domain/domain.go`
- Modify: `internal/adapter/postgres/domain.go`

**Interfaces:**
- Produces: `domain.Metadata.Extract map[string]json.RawMessage` with tag `json:"extract,omitempty"`. The `Trim` field goes.
- Produces: `func (s *Store) SetExtractRule(ctx context.Context, id, key string, rule json.RawMessage) error`

- [ ] **Step 1:** Rename the field.
- [ ] **Step 2:** Add `SetExtractRule`. It runs a single `UPDATE domains SET metadata = metadata || jsonb_build_object('extract', coalesce(metadata->'extract', '{}') || jsonb_build_object($2::text, $3::jsonb)) WHERE id = $1`. Wrap the error.
- [ ] **Step 3:** Run `go vet -overlay $OVERLAY ./internal/core/domain/ ./internal/adapter/postgres/`. Expected: no output.

### Task 2: Split the article rule into content and field rules

**Files:**
- Modify: `internal/adapter/extract/html/rule.go`. `Rule` becomes `ContentRule` and drops `Title`, `Published` and `Author`.
- Modify: `internal/adapter/extract/html/html.go`, renamed to `content.go`. `trimRule` and `trimGeneric` become `contentRule` and `contentGeneric`. The `extraction` struct carries its own `Title`, `Content`, `Text` and `Failures`, without `port.Extraction`.
- Create: `internal/adapter/extract/html/field.go`
- Prompts: rename `system.md` to `content_system.md` and `rule.md` to `content_rule.md`, both without the title, published and author parts. Add `field_system.md`, a template on `.Field` with one instruction block per field, and `field_rule.md` with `selector` and `reasoning`.
- Test: `content_test.go`, renamed from `html_test.go`, plus `field_test.go`, `validate_test.go` and `prompts_test.go`.

**Interfaces:**
- Produces: `type FieldRule struct { Selector *string \`json:"selector" jsonschema:"nullable"\`; Reasoning string \`json:"reasoning"\` }` with `Abstained()`, `sanitize()` and `JSONSchemaExtend`.
- Produces: `type field string` and the constants `fieldTitle = "title"`, `fieldAuthors = "authors"`, `fieldPostedAt = "postedAt"`. Later tasks add `fieldContent = "content"` and `fieldLinks = "links"`.
- Produces: `func readTitle(doc document, rule FieldRule) string`, `func readAuthors(doc document, rule FieldRule) []string`, `func readPostedAt(doc document, rule FieldRule) time.Time`. Each falls back to today's meta chain when the rule abstains or matches nothing.
- Produces: `func validateField(f field, rule FieldRule, heldOut []sample) error`, `func (t *htmlExtractor) generateField(ctx, l, f field, samples []sample) (FieldRule, error)` and `func (t *htmlExtractor) generateContent(ctx, l, samples []sample) (ContentRule, error)`.
- Produces: `type sample struct { URL string; Data []byte }`, replacing `port.Sample`, and `var ErrNoRule`, moved from `port`.
- `contentRule` and `contentGeneric` take the title from `readTitle` for the repeated title check.

- [ ] **Step 1:** Write `TestReadFields` in `field_test.go`.
  - On `site.article(4)`, `.byline a` gives `["Anna Svensson"]`.
  - `time[datetime]` gives day 5.
  - `h1.headline` gives `"Rubrik nummer 4 om inflationen"`.
  - `h2.none` falls back to `h1` and gives the same title.
  - An abstained authors rule gives nil, since the fixture has no author meta.
- [ ] **Step 2:** Write `TestValidateField`.
  - Good selectors pass on `pages(s, 10, 5)`.
  - An authors selector matching only 2 of 5 pages fails.
  - `h1.headline a` fails on title length because it matches nothing.
  - A postedAt selector on a future date fails.
- [ ] **Step 3:** Write `TestGenerateField`.
  - With answers `[abstain, good]`, it returns the good rule.
  - With answers `[abstain, invented]`, it returns an abstained rule and a nil error.
  - With answers `[invented]`, it returns an error wrapping `ErrNoRule` and `ErrSelectorUngrounded`.
  - The field system prompt names the field.
- [ ] **Step 4:** Adapt the existing content tests to `ContentRule`. The repeated title case keeps failing with `FailTitleRepeated`.
- [ ] **Step 5:** Run `go test -overlay $OVERLAY ./internal/adapter/extract/...`. Expected: new tests fail.
- [ ] **Step 6:** Implement. Field generation runs the 3 temperatures and returns the first rule that passes grounding and `validateField`. If none passes and any attempt abstained, it returns an abstained rule. `readPostedAt` reads `datetime`, then `content`, then the text, through the existing `published` layouts.
- [ ] **Step 7:** Run the tests. Expected: PASS.

### Task 3: Article sample pool and generation

**Files:**
- Create: `internal/adapter/extract/html/collect.go`, `collect_test.go`

**Interfaces:**
- Consumes: Task 2 generators, Task 1 key names.
- Produces: `type DomainStore interface { SetExtractRule(ctx context.Context, id, key string, rule json.RawMessage) error }`
- Produces: `func (t *htmlExtractor) collect(ctx context.Context, l *slog.Logger, d domain.Domain, f field, s sample)`. It records the page and marks `f` requested. When the pool reaches 8 and the LLM schedule is active, it takes the semaphore and starts one goroutine running `generate`.
- Produces: `func (t *htmlExtractor) generate(ctx, l, d domain.Domain, fields []field, samples []sample)`. It generates and saves each field in turn. `port.ErrUnavailable` puts the samples back. Other errors set `failedAt[f][d.ID]`.
- State on `htmlExtractor`, all under one mutex: `samples`, `sampledAt`, `sampleSeq`, `requested map[string]map[field]bool`, `failedAt map[field]map[string]time.Time`, and `generating chan struct{}` with capacity 1.

- [ ] **Step 1:** Write `TestCollectOncePerPage`. Calling `collect` for content, title, authors and postedAt with one URL leaves 1 sample and 4 requested fields.
- [ ] **Step 2:** Write `TestCollectOnlyRequested`. Feeding 8 content-only pages with an LLM answering good rules saves only key `content` in a fake store.
- [ ] **Step 3:** Write `TestCollectSkipsFailedField`. A field failed under 24h ago adds no samples when it is the only one requested.
- [ ] **Step 4:** Port `TestCollectEvictsOldestDomain` from `git show HEAD:internal/service/source/trim_test.go` with an idle LLM.
- [ ] **Step 5:** Run the tests. Expected: FAIL.
- [ ] **Step 6:** Implement by porting `collect`, `evictOldestSamples` and `generate` from `git show HEAD:internal/service/source/trim.go`, with per-field requests and failures.
- [ ] **Step 7:** Run the tests. Expected: PASS.

### Task 4: Links in the adapter

**Files:**
- Modify: `internal/adapter/extract/html/links.go`, `links_test.go`

**Interfaces:**
- Produces: `func (t *htmlExtractor) Links(ctx context.Context, l *slog.Logger, d domain.Domain, url string, listing []byte) ([]string, error)`
- `htmlLinker` folds into `htmlExtractor`. `port.Fetch` becomes the local `type fetch func(ctx context.Context, url string) ([]byte, error)`. The extractor holds a `safehttp` client with a 20s timeout and fetches through `fetcher.FetchHTML` with `logs.Client(client, l, "extract")`.

- [ ] **Step 1:** Port `TestLinksFromOneListingFailureCollects` and `TestLinksUnavailableRetries` from `git show HEAD:internal/service/source/trim_test.go` against `htmlExtractor`. Swap `failingLinker` for a `fakeLLM` that answers an abstain, or for an LLM returning `port.ErrUnavailable`.
- [ ] **Step 2:** Run the tests. Expected: FAIL.
- [ ] **Step 3:** Implement by porting `Links`, `withoutRule`, `fromOneListing`, `collectListing` and `generateLinks` from `git show HEAD:internal/service/source/links.go`. Rules save through `SetExtractRule` with key `links`.
- [ ] **Step 4:** Run the tests. Expected: PASS.

### Task 5: Port and field methods

**Files:**
- Modify: `internal/core/port/extract.go`
- Create: `internal/adapter/extract/html/extractor.go` for the `htmlExtractor` struct, `New` and the content, title, authors and postedAt methods.

**Interfaces:**
- Produces: `port.Extractor`, exactly as in the spec. `RuleExtractor`, `Linker`, `Fetch`, `Sample`, `Extraction` and `ErrNoRule` leave `port`.
- Produces: `func New(llm port.LLM, domains DomainStore, l *slog.Logger) port.Extractor`
- Each method reads its rule from `d.Metadata.Extract[key]`. A missing or unreadable rule calls `collect`.
- `Content` returns `fmt.Errorf("err content checks: %v", failures)` when checks fail.

- [ ] **Step 1:** Write `TestExtractorFields`. A domain with stored good rules for all four keys returns body text without promo, title, `["Anna Svensson"]` and day 5, with no sample collected. A domain with no rules returns generic content and collects one sample.
- [ ] **Step 2:** Run the tests. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test -overlay $OVERLAY ./internal/adapter/extract/... && go vet -overlay $OVERLAY ./internal/core/port/`. Expected: PASS.

### Task 6: Core extract step and page fetcher

**Files:**
- Modify: `internal/core/service/source/trim.go`, renamed to `extract.go`
- Modify: `internal/core/service/source/sourced.go`, where `s.Trim` becomes `s.Extract`
- Modify: `internal/adapter/fetcher/page.go`
- Modify: `cmd/main.go`, the extractor constructor line only
- Test: `internal/core/service/source/extract_test.go`

**Interfaces:**
- Consumes: `port.Extractor`.
- Produces: `func (s *Service) Extract(ctx context.Context, l *slog.Logger, p domain.RawPost) (domain.RawPost, error)`
- Page fetcher: `Linker` becomes `Links(ctx, l, d domain.Domain, url string, listing []byte) ([]string, error)`. `NewPage` gains a `Domains interface{ GetOrCreateDomain(ctx context.Context, rawURL string) (domain.Domain, error) }` parameter.

- [ ] **Step 1:** Write `TestExtractKeepsFetched`. With a fake extractor, a post with title, authors and postedAt keeps all three, and `Title`, `Authors` and `PostedAt` are not called. A post without them takes the extracted values. A `Content` error keeps the fetched content. The test calls the unexported `extract(ctx, l, d, p)` so it needs no store.
- [ ] **Step 2:** Run the test. Expected: FAIL.
- [ ] **Step 3:** Implement. `Extract` resolves the domain and calls `extract`.
- [ ] **Step 4:** Run `go test -overlay $OVERLAY ./internal/core/... && go vet -overlay $OVERLAY ./internal/adapter/fetcher/`. Expected: PASS.

### Task 7: Docs

- [ ] Update `docs/kb/architecture.md` step 5 and `docs/kb/discovered-ingest.md`, which mentions `Trim.Apply`, to name the extractor fields.
