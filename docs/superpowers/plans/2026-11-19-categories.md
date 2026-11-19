# Categories Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Seeded categories that group tags, two-stage Jev tagging, categories for proposed tags, a category feed filter, and admin management.

**Architecture:** Migration `002_categories.sql` adds `categories`, `category_terms` and `category_tags` and seeds 18 categories and 137 tags. Enrich asks Jev about categories first, then only the tags of matched categories plus uncategorized ones. A post's categories derive from its tags. Admin routes and pages build on the admin SPA.

**Tech Stack:** Go, pgx v5, tern migrations, Postgres, SvelteKit SPA with shadcn-svelte.

**Spec:** `docs/superpowers/specs/2026-11-19-categories-design.md`

## Global Constraints

- `001_init.sql` is never edited. All schema and seed changes go in `internal/migrations/002_categories.sql`. No `{{` in SQL.
- Seed values are copied verbatim from the spec's Categories and Tags tables.
- Category threshold 0.5. Tag threshold 0.8. 50 questions per Jev request. Cache TTL 1 minute.
- Category keys: 1 to 40 characters, normalized with `discovery.TagKey`. Category terms: required for `sv` and `en`, at most 100 characters.
- The system never creates or enables a category. Seeding never changes an existing tag's `enabled`.
- Go style per `~/.claude/CLAUDE.md`: `any`, wrapped errors `fmt.Errorf("err <what>: %w", err)` in leaf calls, guard clauses, default assignment.
- Go DB tests need `TEST_POSTGRES_CONN`, for example `postgresql://postgres:<pw>@localhost:5432/postgres?sslmode=disable` with the dev compose db up. Without it they skip, so run them with it set.
- Tasks 1 to 6 run now. Tasks 7 and 8 need the admin SPA plan, `docs/superpowers/plans/2026-11-14-admin-spa.md`, executed first. Stop after Task 6 if `internal/core/service/admin` does not exist.

## Review Focus

1. Jev answers without a score for a category question. Expect a score of 0: the category does not match, uncategorized tags are still asked. Task 4.
2. A tag in two matched categories. Expect it asked once, not twice. Task 4.
3. DeepSeek returns category keys like `"Energy "` or `"real estate"`. Expect them normalized with `TagKey` and kept. Task 5.
4. `?category=` combined with `?tag=` and `after`, and a category whose only tags are disabled. Expect all filters to apply together, and an empty list for the latter. Task 6.
5. Admin creates category `"Real Estate"`. Expect it normalized to `real_estate` and a 409 against the seed. Task 7.

---

### Task 1: Migration, seed and test helpers

**Files:**
- Create: `internal/migrations/002_categories.sql`
- Modify: `internal/migrations/migrations.go`
- Modify: `internal/lib/dbtest/dbtest.go`
- Create: `internal/migrations/migrations_test.go`, package `migrations_test`

**Interfaces:**
- Produces: `migrations.MigrateTo(ctx context.Context, connString string, l *slog.Logger, version int32) error`. `Migrate` keeps its signature and migrates to the latest version.
- Produces: `dbtest.Empty(t *testing.T) string`, which creates an unmigrated database, registers its drop, and returns its conn string. It skips like `New`.
- Produces: `dbtest.Seeded(t *testing.T) *database.Database`, a migrated database with the seed kept.
- Changes: `dbtest.New` now empties `category_tags`, `category_terms`, `categories`, `tag_aliases`, `tag_terms` and `tags` after migrating. Existing tests then keep their own fixtures. Comment why in one line.

- [ ] **Step 1: Write failing tests in `migrations_test.go`**
  - `TestSeed` on `dbtest.Seeded`:
    - 18 categories, all enabled, each with one `sv` and one `en` term.
    - 137 tags, all `active`, `enabled` and `quality`, each with an `sv` and an `en` term.
    - No tag without a `category_tags` row.
    - `batteries` maps to exactly `energy`, `automotive` and `industry`.
    - `category_terms` for `healthcare` in `sv` is `hälsa och life science`.
  - `TestSeedExistingTags` on `dbtest.Empty`, then `MigrateTo(…, 1)`. Insert:
    - `('dividend', 'proposed', enabled false)`,
    - tag `acquisition_deal` as `proposed`, with alias `('acquisition', 'acquisition_deal')`.

    Then `MigrateTo(…, 2)` and assert:
    - `dividend` is `active` and still disabled.
    - No `acquisition` row in `tags`.
    - `acquisition_deal` is `active` and has `category_tags` row `deals`.
    - `tag_terms` holds `('acquisition_deal', 'sv', 'förvärv')`.
- [ ] **Step 2: Run** `go test ./internal/migrations/ -run TestSeed -v`. Expect FAIL: `MigrateTo`, `Empty` and `Seeded` are undefined.
- [ ] **Step 3: Implement**
  - `MigrateTo` and `Migrate` share one helper. `MigrateTo` calls tern's `m.MigrateTo(ctx, version)`.
  - `dbtest.New` is built from `Empty` plus `Migrate` plus the emptying step.
  - Write `002_categories.sql`:
    - The spec's three tables and its index.
    - Then the seed through a temp table `seed_tags (key, sv, en, categories TEXT[])` dropped on commit.
    - Resolve each key with `COALESCE((SELECT key FROM tag_aliases WHERE alias = s.key), s.key)`.
    - Insert `SELECT DISTINCT` resolved keys into `tags` with `ON CONFLICT (key) DO UPDATE SET status = 'active'`.
    - Insert terms and `category_tags` with `ON CONFLICT DO NOTHING`.
- [ ] **Step 4: Run** `go test ./...`. Expect PASS, including every existing test.
- [ ] **Step 5: Commit** `Add categories tables and seed`

### Task 2: Category reads and tag categories

**Files:**
- Create: `internal/core/domain/category.go`
- Modify: `internal/core/domain/tag.go`
- Create: `internal/adapter/postgres/category.go`, `internal/adapter/postgres/category_test.go`
- Modify: `internal/adapter/postgres/tag.go`, `internal/adapter/postgres/tag_test.go`

**Interfaces:**
- Produces in `domain`:
  - `Category{Key string; Enabled bool; Terms map[string]string; Tags []string; CreatedAt time.Time}`, with JSON `key, enabled, terms, tags, createdAt`. `Terms` maps lang to term.
  - `Tag` gains `Categories []string` with JSON `categories`.
- Produces in `postgres`:
  - `CategoryStore`, built with `NewCategoryStore(db)`.
  - `CategoryStore.EnabledCategories(ctx) ([]domain.Category, error)`. Enabled categories, ordered by key, with all terms. `Tags` holds the mapped tag keys that are enabled and active, ordered by key and never nil.
  - `TagStore.ActiveTags` fills `Categories` with the tag's enabled category keys, ordered, never nil.

- [ ] **Step 1: Write failing tests**
  - `TestEnabledCategories`:
    - Seed categories `finance` and `energy`, plus a disabled `macro`. `finance` has terms sv and en.
    - Map `finance` to an active tag, a proposed tag and a disabled tag.
    - Expect `[energy, finance]`. `finance.Terms` is `{"sv": …, "en": …}` and `finance.Tags` is only the active tag. `energy.Tags` is `[]`.
  - Extend `TestActiveTags`:
    - Map `acquisition` to an enabled category and a disabled one.
    - Expect `Categories` to be only the enabled one. `contract.Categories` is `[]`.
- [ ] **Step 2: Run** `go test ./internal/adapter/postgres/ -run 'TestEnabledCategories|TestActiveTags' -v`. Expect FAIL.
- [ ] **Step 3: Implement** both queries with `array_agg … FILTER`, as `ActiveTags` already does.
- [ ] **Step 4: Run** `go test ./internal/adapter/postgres/`. Expect PASS.
- [ ] **Step 5: Commit** `Read enabled categories and each tag's categories`

### Task 3: Categories on new proposed tags

**Files:**
- Modify: `internal/adapter/postgres/tag.go`, `internal/adapter/postgres/tag_test.go`

**Interfaces:**
- Changes: `Proposal` gains `Categories []string` with JSON `categories`.
- Behavior: `SaveProposals` keeps its signature. It writes `category_tags` for a proposal only when its own `INSERT INTO tags` created the row, detected with `RETURNING key`. Only existing enabled categories in `Categories` are written, `ON CONFLICT DO NOTHING`. Keys are used as given, since enrich normalizes them.

- [ ] **Step 1: Write failing test `TestSaveProposalCategories`**
  - Setup: `tagDB` plus categories `energy` and `automotive` enabled, and `macro` disabled.
  - A proposal of new key `solid_state_battery` with `["energy", "automotive", "macro", "nope"]` maps it to exactly `automotive` and `energy`.
  - A second save of the same key with `["macro"]` changes nothing.
  - After deleting its rows by hand, a third save with `["energy"]` still adds nothing. This is the cleared case.
  - `new_factory`, already proposed, with `["energy"]` gets no rows.
  - `takeover`, an alias of active `acquisition`, with `["energy"]` gets no rows.
- [ ] **Step 2: Run** `go test ./internal/adapter/postgres/ -run TestSaveProposalCategories -v`. Expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/adapter/postgres/`. Expect PASS, including `TestSaveProposalsConcurrent`.
- [ ] **Step 5: Commit** `Set categories on tags that a proposal creates`

### Task 4: Two-stage Jev

**Files:**
- Modify: `internal/core/service/enrich/tags.go`, `internal/core/service/enrich/tags_test.go`
- Modify: `internal/core/service/enrich/cache.go`, `internal/core/service/enrich/cache_test.go`
- Modify: `internal/core/service/enrich/enrich.go`, `internal/core/service/enrich/enrich_test.go`
- Modify: `cmd/main.go`

**Interfaces:**
- Consumes: `CategoryStore.EnabledCategories`, and `Tag.Categories` from Task 2.
- Changes:
  - `jevTags(ctx context.Context, c port.Classifier, l *slog.Logger, lang, title, markdown string, categories []domain.Category, tags []domain.Tag) ([]string, error)`.
  - `tagSet` gains `categories []domain.Category`. `loadTags` reads it. The cache key stays per language.
  - `Service` gains `Categories *postgres.CategoryStore`, wired in `main.go` with a new `categoryStore := postgres.NewCategoryStore(db)`.
- Constants: `categoryThreshold = 0.5`.
- Behavior:
  - Stage 1 runs only when `categories` is non-empty. It sends one request per 50 categories, with IDs `category_%d`. The question is `"Is this news article about " + key`, plus `" (" + Terms[lang] + ")"` when that term exists, plus `"?"`.
  - Stage 2 candidates are tags with no `Categories`, or with any category that matched. Each candidate is asked once, in the input order, with today's batching and IDs.
  - A missing score is 0.

- [ ] **Step 1: Write failing tests** in `tags_test.go`:
  - `TestJevTagsTwoStage`:
    - Categories `energy` with an sv term, and `finance`.
    - Tags: `oil` in energy, `dividend` in finance, `batteries` in energy and finance, `misc` with none.
    - Scores: `energy` 0.5, `finance` 0.49, `oil` 0.9, `batteries` 0.9, `misc` 0.8, `dividend` 0.99.
    - Expect `[oil, batteries, misc]`.
    - The first request has 2 questions, and the energy question contains `(energi)`.
    - The second request asks `oil`, `batteries` and `misc`, and not `dividend`.
  - `TestJevTagsNoCategories`: with nil categories, one request covering every tag, as today.
  - **Review Focus 1:** `TestJevTagsMissingCategoryScore`: the fake answers no score for `energy`. Expect `oil` not asked and `misc` asked.
  - **Review Focus 2:** `TestJevTagsTagInTwoCategories`: both categories score 0.9. Expect `batteries` to appear once in the stage 2 request and once in the result.
  - `TestJevTagsCategoryError`: a stage 1 error wraps `port.ErrUnavailable` and makes no stage 2 request.
  - Existing `TestJevTags*` tests pass `nil` categories.
  - `enrich_test.go`: `setup` seeds category `deals` and maps `acquisition` to it. The fake scores `deals` 0.9. `TestEnrich` still expects `acquisition` on the post.
  - New `TestEnrichCategoryMiss`: `deals` scores 0. Expect the post without `acquisition`.
- [ ] **Step 2: Run** `go test ./internal/core/service/enrich/ -v`. Expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./... && go vet ./...`. Expect PASS.
- [ ] **Step 5: Commit** `Ask Jev about categories before tags`

### Task 5: Categories in the enrich prompt

**Files:**
- Modify: `internal/core/service/enrich/prompts/enrich.md`
- Modify: `internal/core/service/enrich/prompt.go`, `internal/core/service/enrich/prompt_test.go`
- Modify: `internal/core/service/enrich/enrich.go`, `internal/core/service/enrich/enrich_test.go`

**Interfaces:**
- Changes: `userPrompt(lang string, keys, qualityKeys, categoryKeys []string, markdown string) (string, error)`. It adds the line `- Categories: <keys joined by ", ">`, or `none`.
- Behavior in `enrich`: each proposal's `Categories` go through `discovery.TagKey` before `SaveProposals`. Empty results are dropped.
- Prompt copy for the tags section of `enrich_system_prompt`:
  - Examples become `acquisition, merger, contract, layoffs, quarterly_report, dividend, share_buyback, ipo, bankruptcy or new_factory`.
  - The key example becomes `like acquisition or share_buyback`.
  - Add the field: `categories: one or more of the listed categories the tag belongs to, using their keys exactly. An empty list when none fits.`

- [ ] **Step 1: Write failing tests**
  - `TestSystemPrompt` also requires `categories:` and `quarterly_report`, and rejects `earnings_report`.
  - `TestUserPrompt` passes `[]string{"energy", "finance"}` and requires `Categories: energy, finance`. A nil list gives `Categories: none`.
  - **Review Focus 3:** `TestEnrichProposalCategories`:
    - Category `energy` and `real_estate` are enabled.
    - DeepSeek proposes a new key with `["Energy ", "real estate"]`.
    - Expect the new tag mapped to both.
- [ ] **Step 2: Run** `go test ./internal/core/service/enrich/ -v`. Expect FAIL.
- [ ] **Step 3: Implement.** Category keys come from `tagSet.categories`.
- [ ] **Step 4: Run** `go test ./...`. Expect PASS.
- [ ] **Step 5: Commit** `Let enrich put proposed tags into categories`

### Task 6: Public category filter, categories route, merge and docs

**Files:**
- Modify: `internal/core/domain/post.go`
- Modify: `internal/adapter/postgres/post.go`, `internal/adapter/postgres/post_test.go`
- Modify: `internal/core/service/posts/route.go`
- Create: `internal/core/service/categories/route.go`, `internal/core/service/categories/route_test.go`
- Modify: `cmd/main.go`
- Modify: `frontend/src/routes/admin/admin.remote.ts`, function `mergeTag`
- Create: `docs/kb/categories.md` from `docs/kb/_template.md`
- Modify: `docs/kb/index.md`

**Interfaces:**
- Consumes: `CategoryStore.EnabledCategories` from Task 2.
- Produces:
  - `PostFilter.Categories []string` with tag `query:"category"`. `listPosts` reads `q["category"]`.
  - `categories.Routes(mux *http.ServeMux, s Store, l *slog.Logger)`, where `Store` has `EnabledCategories`. It registers `GET /categories` and answers `[]domain.Category`, never null.
- Behavior:
  - Each category adds the spec's filter: `v.tags && ARRAY(SELECT ct.tag FROM category_tags ct JOIN categories c ON c.key = ct.category WHERE c.key = $n AND c.enabled)`.
  - `mergeTag` runs `INSERT INTO category_tags (category, tag) SELECT category, ${to} FROM category_tags WHERE tag = ${from} ON CONFLICT DO NOTHING` before it deletes `from`.

- [ ] **Step 1: Write failing tests**
  - `TestListPostsCategory`:
    - Two categories and posts tagged across them.
    - One category gives its posts.
    - Two categories give only posts in both.
    - A disabled or unknown key gives none.
    - A post carrying only a proposed tag of the category is left out.
  - **Review Focus 4:** `TestListPostsCategoryWithTagAndAfter`:
    - `Categories`, `Tags` and `After` combined return only the matching post after the cursor.
    - A category whose only tag is disabled returns none.
  - `TestCategoriesRoute`:
    - A fake store with one category gives 200, camelCase JSON and `terms`.
    - An empty store gives `[]`.
    - A store error gives 500.
- [ ] **Step 2: Run** `go test ./internal/adapter/postgres/ ./internal/core/service/categories/ -v`. Expect FAIL.
- [ ] **Step 3: Implement**, then register `categories.Routes(mux, &categoryStore, l.WithGroup("categories"))` next to `posts.Routes`. Patch `mergeTag`.
- [ ] **Step 4: Run** `go test ./...` and `pnpm -C frontend check`. Expect PASS. In the dev admin, merge two tags where `from` has a category, and check that `to` has it after.
- [ ] **Step 5: Write `docs/kb/categories.md`**:
  - Summary, current state, the two-stage flow and the defaults table from the spec.
  - Add a row to `docs/kb/index.md`.
- [ ] **Step 6: Commit** `Filter the feed by category and list categories`

### Task 7: Admin category routes

Requires admin SPA plan Tasks 3 to 6.

**Files:**
- Modify: `internal/core/domain/admin.go`, `internal/adapter/postgres/category.go`, `internal/adapter/postgres/category_test.go`
- Modify: `internal/adapter/postgres/tag.go`, `internal/adapter/postgres/tag_test.go`, `internal/adapter/postgres/post.go`, `internal/adapter/postgres/post_test.go`
- Create: `internal/core/service/admin/categories.go`, `internal/core/service/admin/categories_test.go`
- Modify: `internal/core/service/admin/route.go`, `internal/lib/cors/cors.go`, `internal/lib/cors/cors_test.go`, `cmd/main.go`

**Interfaces:**
- Produces in `domain`:
  - `AdminCategory{Category; TagCount, PostCount int}` with JSON `tagCount, postCount`.
  - `NewCategory{Key, Sv, En string}` with JSON `key, sv, en`.
  - `ErrCategoryExists = errors.New("Category already exists")`.
  - `ErrUnknownCategory = errors.New("Unknown category")`.
  - `TagSummary` gains `Categories []string`, filled from all of the tag's categories, enabled or not.
  - `AdminPostFilter` gains `Category string`.
- Produces in `postgres`:
  - `CategoryStore.ListAdminCategories(ctx) ([]domain.AdminCategory, error)`. All categories. `PostCount` counts posts where `p.tags` overlaps the category's tags, on all tags.
  - `CategoryStore.CreateCategory(ctx, c domain.NewCategory) error`. Returns `ErrCategoryExists` on a key conflict.
  - `CategoryStore.SetCategoryEnabled(ctx, key string, enabled bool) error`. Wraps `pgx.ErrNoRows` for an unknown key.
  - `CategoryStore.SetTagCategories(ctx, tag string, categories []string) error`. Replaces the rows in one transaction. Wraps `pgx.ErrNoRows` for an unknown tag, and returns `ErrUnknownCategory` when any key is not a category, before writing.
  - `ListAdminPosts` with `Category` matches `p.tags && ARRAY(SELECT tag FROM category_tags WHERE category = $n)`.
  - Go `MergeTag` copies `category_tags` as in Task 6.
- Produces in `admin`, with `Stores.Categories *postgres.CategoryStore`, routes per the spec's admin table.
  - Validation: `key` is normalized with `TagKey` and must be 1 to 40 characters. `sv` and `en` are trimmed, required, and at most 100 characters.
  - `PUT /admin/tags/{key}/categories` normalizes each key the same way.
- CORS preflight methods become `GET, POST, PATCH, PUT`.

- [ ] **Step 1: Write failing tests**
  - `TestListAdminCategories`: a disabled category is listed. Counts include a post carried only by a proposed tag.
  - `TestCreateCategory`: a duplicate gives `ErrCategoryExists`.
  - `TestSetTagCategories`:
    - Replacing gives the new set.
    - `[]` clears it.
    - An unknown category gives `ErrUnknownCategory` and leaves the rows unchanged.
    - An unknown tag gives `pgx.ErrNoRows`.
  - `TestMergeTag` gains: `to` has `from`'s categories after the merge.
  - `TestListAdminPosts` gains a `Category` case.
  - `TestTagOverview` gains `Categories`.
  - `TestCategoryRoutes`:
    - Create gives 201.
    - Missing `en` gives 400.
    - A 41-character key gives 400.
    - **Review Focus 5:** with the seed loaded through `dbtest.Seeded`, `{"key":"Real Estate",…}` gives 409.
    - `PATCH` of an unknown key gives 404.
    - `PUT /admin/tags/acquisition/categories` with `["nope"]` gives 400 `Unknown category`, and with an unknown tag gives 404.
  - The CORS test expects `PUT` in the preflight.
- [ ] **Step 2: Run** `go test ./internal/... -v`. Expect FAIL.
- [ ] **Step 3: Implement**, and set `Stores.Categories` in `main.go`.
- [ ] **Step 4: Run** `go test ./... && go vet ./...`. Expect PASS.
- [ ] **Step 5: Commit** `Add admin category routes`

### Task 8: Admin SPA categories

Requires admin SPA plan Tasks 8, 10 and 11. Use the `svelte:svelte-file-editor` agent for `.svelte` files.

**Files:**
- Create: `admin/src/routes/(app)/categories/+page.ts`, `categories/+page.svelte`
- Modify: `admin/src/routes/(app)/tags/+page.svelte`, `tags/+page.ts`, `admin/src/routes/(app)/posts/+page.ts`, `posts/+page.svelte`, and the sidebar nav in the app layout

**Interfaces:**
- Consumes: Task 7 routes, and the SPA's API helpers.

Behavior:
- **Categories page:**
  - A table with key, sv and en terms, tag count and post count.
  - A `Switch` for enabled. Disabled rows are dimmed.
  - The key links to `/posts?category=<key>`, and the tag count to `/tags?category=<key>`.
  - A create form with key, sv and en that shows the server message on error.
  - Loads with `depends('app:categories')`.
- **Tags page:**
  - A categories column holding a multi-select that `PUT`s on change.
  - A category filter read from `?category=`.
- **Posts page:**
  - A `category` param and a removable chip, like `tag`.

- [ ] **Step 1: Implement.**
- [ ] **Step 2: Verify** with `pnpm -C admin check` and `pnpm -C admin lint`. In the browser:
  - Create a category and see it listed.
  - Disable it and reload. It stays dimmed.
  - Set a tag's categories and reload. They persist.
  - `/posts?category=finance` narrows the list, and the chip clears it.
- [ ] **Step 3: Commit** `Add categories to the admin SPA`
