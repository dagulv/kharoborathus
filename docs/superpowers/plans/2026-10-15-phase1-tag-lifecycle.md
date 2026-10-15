# Phase 1: Tag Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** New tags only go live after independent trusted posts agree, duplicates become aliases, broad tags are marked unable to gate, unused tags go dormant, and sources get an `enabled` switch instead of `active`.

**Architecture:** `TagGenerator` output is routed into new terms for known tags, reactivations, or rows in a new `tag_proposals` table. An hourly promotion job turns proposals with enough support into tags after an LLM duplicate check and Jev consistency and broadness checks. A daily maintenance job recomputes match rates from `posts.tags` and marks unused tags dormant. Admin gets a tags page.

**Tech Stack:** Go 1.27, pgx v5, Postgres 18, DeepSeek through `adapter.LLM`, Jev through `adapter.Classifier`, SvelteKit 3 remote functions, postgres.js.

**Spec:** `docs/kb/tag-lifecycle.md`, `docs/kb/source-lifecycle.md` for the rename, `docs/kb/rollout.md` phase 1.

## Global Constraints

- Every post is trusted in phase 1. Do not add `origin`, source `status` or trust checks. They arrive in phase 4.
- Schema changes go in `internal/migrations/001_tag_lifecycle.sql`, which must be safe to run twice. `internal/migrations/schema.sql` is updated for fresh databases.
- Promotion thresholds: 3 supporting posts, 2 distinct domains, 30-day support window, at most 5 candidates per run.
- Consistency: at least two thirds of supporting posts must match the candidate.
- Broadness and backfill set: posts from the last 7 days, at most 200. `gate = true` only when the match rate is below 0.2 and the set has at least 20 posts.
- Daily recompute uses 30 days of `posts.tags`. Dormancy after 90 days without a match.
- Go style: `any` not `interface{}`, guard clauses, wrap leaf errors as `fmt.Errorf("err <action>: %w", err)`, comments only for gotchas.
- Use `pnpm`, never `npx`.
- One commit per task. End each commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.

## Review Focus

- A newly promoted tag on a quiet week, with fewer than 20 recent posts, must get `gate = false`, never a rate-based `true`. Pinned in Task 7.
- The generator suggesting a tag you disabled must not propose, reactivate or add terms to it. Pinned in Task 5.
- The generator returning an alias and its canonical key in one answer must produce one entry, with the alias term stored under the canonical key. Pinned in Task 5.
- A candidate whose supporting posts were all deleted must be dropped, not divide by zero or promote. Pinned in Task 7.
- Running the migration twice, or `schema.sql` on an empty database, must succeed. Pinned in Task 1.

---

### Task 1: Migration and schema

**Files:**
- Create: `internal/migrations/001_tag_lifecycle.sql`
- Modify: `internal/migrations/schema.sql` around the `tags` table and the `sources` table

**Interfaces:**
- Produces: `sources.enabled`, `tags.enabled`, `tags.status`, `tags.gate`, `tags.match_rate`, `tags.last_matched_at`, `tags.created_at`, tables `tag_proposals` and `tag_aliases`.

- [ ] **Step 1: Write the migration**

`internal/migrations/001_tag_lifecycle.sql`:

```sql
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_name = 'sources' AND column_name = 'active'
  ) THEN
    ALTER TABLE sources RENAME COLUMN active TO enabled;
    ALTER INDEX sources_active_idx RENAME TO sources_enabled_idx;
  END IF;
END $$;

ALTER TABLE tags ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'; -- active | dormant
ALTER TABLE tags ADD COLUMN IF NOT EXISTS gate BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS match_rate REAL;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS last_matched_at TIMESTAMPTZ;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS tag_proposals (
  key TEXT NOT NULL,
  lang TEXT NOT NULL,
  term TEXT NOT NULL,
  post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  source_id UUID NOT NULL REFERENCES sources (id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (key, post_id)
);

CREATE INDEX IF NOT EXISTS tag_proposals_created_at_idx ON tag_proposals (created_at);

CREATE TABLE IF NOT EXISTS tag_aliases (
  alias TEXT PRIMARY KEY,
  key TEXT NOT NULL REFERENCES tags (key) ON DELETE CASCADE
);
```

- [ ] **Step 2: Update schema.sql for fresh databases**

Replace `CREATE TABLE IF NOT EXISTS tags (key TEXT PRIMARY KEY);` with:

```sql
CREATE TABLE IF NOT EXISTS tags (
  key TEXT PRIMARY KEY,
  enabled BOOLEAN NOT NULL DEFAULT true,
  status TEXT NOT NULL DEFAULT 'active', -- active | dormant
  gate BOOLEAN NOT NULL DEFAULT false,
  match_rate REAL,
  last_matched_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

In the `sources` table replace `active BOOLEAN NOT NULL DEFAULT false,` with `enabled BOOLEAN NOT NULL DEFAULT false,`, and replace `CREATE INDEX IF NOT EXISTS sources_active_idx ON sources (active);` with `CREATE INDEX IF NOT EXISTS sources_enabled_idx ON sources (enabled);`.

Append at the end of the file the `tag_proposals` table, its index and the `tag_aliases` table, copied exactly from Step 1.

- [ ] **Step 3: Apply the migration twice**

Run:

```bash
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < internal/migrations/001_tag_lifecycle.sql
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < internal/migrations/001_tag_lifecycle.sql
```

Expected: both runs exit 0. The second prints `NOTICE ... already exists, skipping` lines only.

- [ ] **Step 4: Check the result**

Run:

```bash
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "\d tags" -c "\d sources" -c "\d tag_proposals" -c "\d tag_aliases"'
```

Expected: `tags` has the six new columns, `sources` has `enabled` and no `active`, both new tables exist.

- [ ] **Step 5: Check schema.sql on an empty database**

Run:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" schema_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d schema_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" schema_check'
```

Expected: all three exit 0.

- [ ] **Step 6: Commit**

```bash
git add internal/migrations/001_tag_lifecycle.sql internal/migrations/schema.sql
git commit -m "Add tag lifecycle migration"
```

---

### Task 2: Rename source active to enabled in Go

**Files:**
- Modify: `internal/service/source/model.go:15,25`
- Modify: `internal/service/source/store.go:20,45-47,97-100,110,116,139`
- Modify: `internal/service/source/sourced.go:144-147,167-175`

**Interfaces:**
- Consumes: `sources.enabled` from Task 1.
- Produces: `source.Source.Enabled bool` with JSON `enabled`, `source.Filter.Enabled sql.Null[bool]`, function `enabledSources`.

- [ ] **Step 1: Rename the fields**

In `model.go`:

```go
	Enabled    bool              `json:"enabled"`
```

replacing the `Active` field of `Source`, and in `Filter`:

```go
	Enabled sql.Null[bool] `json:"enabled"`
```

- [ ] **Step 2: Update the store**

In `store.go` replace `active,` in `sourceColumns` with `enabled,`. Replace the filter block with:

```go
		if filter.Enabled.Valid {
			args = append(args, filter.Enabled.V)
			where += fmt.Sprintf(` AND enabled = $%d`, len(args))
		}
```

In `CreateSource` replace `active` in the column list with `enabled` and `source.Active` with `source.Enabled`. In `UpdateSource` replace `active = $4,` with `enabled = $4,` and `source.Active` with `source.Enabled`. In `scanSource` replace `&source.Active,` with `&source.Enabled,`.

- [ ] **Step 3: Update the poller**

In `sourced.go` rename `activeSources` to `enabledSources` at its definition and its call, rename the local `active` variable to `enabled`, and replace the filter line with:

```go
			Enabled: sql.Null[bool]{V: true, Valid: true},
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no errors, all tests PASS.

Run: `grep -rn "\.Active\b\|activeSources\|\bactive\b" internal/service/source | grep -v "Schedule().Active"`
Expected: no output. The `Schedule().Active(...)` calls in `links.go` and `trim.go` are unrelated and stay.

- [ ] **Step 5: Commit**

```bash
git add internal/service/source
git commit -m "Rename source active flag to enabled"
```

---

### Task 3: Rename source active to enabled in admin

**Files:**
- Modify: `frontend/src/lib/types.ts:7`
- Modify: `frontend/src/routes/admin/admin.remote.ts:15-19,39,148,151,179-180`
- Modify: `frontend/src/routes/admin/+page.svelte:12`
- Modify: `frontend/src/routes/admin/sources/+page.svelte:75-78`
- Modify: `frontend/src/routes/admin/sources/new/+page.svelte:4,65-66`

**Interfaces:**
- Consumes: `sources.enabled` from Task 1.
- Produces: `Source.enabled: boolean`, `getStats()` field `enabledSources`.

- [ ] **Step 1: Update types and queries**

`types.ts`: `active: boolean;` becomes `enabled: boolean;`.

`admin.remote.ts`:
- `getStats` type `activeSources: number` becomes `enabledSources: number`, and the SQL line becomes `(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL AND enabled) AS enabled_sources,`.
- `listSources`: `s.active,` becomes `s.enabled,`.
- `createSource` schema: `active: v.optional(v.boolean(), false),` becomes `enabled: v.optional(v.boolean(), false),`, the handler destructures `enabled` instead of `active`, and the insert becomes:

```ts
				INSERT INTO sources (domain_id, adapter, url, enabled, rate, target_tags)
				VALUES (${domain.id}, ${adapter}, ${url}, ${enabled}, ${rate}, ${tags}::text[])
```

- [ ] **Step 2: Update the pages**

`+page.svelte`: `['Active sources', stats.activeSources]` becomes `['Enabled sources', stats.enabledSources]`.

`sources/+page.svelte`:

```svelte
						{:else if source.enabled}
							<span class="rounded bg-green-100 px-1.5 py-0.5 text-xs text-green-700">enabled</span>
						{:else}
							<span class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-600">disabled</span>
						{/if}
```

`sources/new/+page.svelte`: destructure `enabled` instead of `active` from `createSource.fields`, and:

```svelte
	<label class="flex items-center gap-2">
		<input {...enabled.as('checkbox')} />
		<span class="text-sm">Enabled</span>
	</label>
```

- [ ] **Step 3: Type check**

Run: `cd frontend && pnpm check`
Expected: `svelte-check found 0 errors`.

Run: `grep -rn "\.active\b\|activeSources\|active_sources" frontend/src`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add frontend/src
git commit -m "Rename source active flag to enabled in admin"
```

---

### Task 4: Topic-only generator prompt and tag merger

**Files:**
- Modify: `internal/service/robot/prompts/tag_generate.md`
- Create: `internal/service/robot/prompts/tag_merge.md`
- Create: `internal/service/robot/tag_merger.go`
- Test: `internal/service/robot/tag_generator_test.go`, `internal/service/robot/tag_merger_test.go`

**Interfaces:**
- Consumes: `adapter.LLM`, `robot.Tag{Key string; Terms []string}`.
- Produces: `robot.NewTagMerger(llm adapter.LLM) *TagMerger`, `(*TagMerger).Same(ctx context.Context, a, b Tag) (bool, error)`. Errors wrap the LLM error, so `errors.Is(err, adapter.ErrUnavailable)` holds.

- [ ] **Step 1: Write the failing tests**

Append to `tag_generator_test.go`:

```go
func TestTagGeneratePromptTopicOnly(t *testing.T) {
	llm := &fakeLLM{}

	if _, err := NewTagGenerator(&tagLLM{fakeLLM: llm}).Generate(context.Background(), "sv", "Volvo investerar.", nil); err != nil {
		t.Fatal(err)
	}

	if s := llm.req.System; strings.Contains(s, "market direction and sentiment:") || !strings.Contains(s, "No sentiment or market direction") {
		t.Errorf("system prompt %q", s)
	}
}
```

Create `tag_merger_test.go`:

```go
package robot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

type mergeLLM struct {
	*fakeLLM
	same bool
	err  error
}

func (f *mergeLLM) Unmarshal(_ context.Context, req adapter.Request, out any) error {
	f.fakeLLM.req = req

	if f.err != nil {
		return f.err
	}

	b, err := json.Marshal(mergeAnswer{Same: f.same})
	if err != nil {
		return err
	}

	return json.Unmarshal(b, out)
}

func TestSameTag(t *testing.T) {
	llm := &mergeLLM{fakeLLM: &fakeLLM{}, same: true}

	same, err := NewTagMerger(llm).Same(context.Background(),
		Tag{Key: "investments", Terms: []string{"investments", "investeringar"}},
		Tag{Key: "investment", Terms: []string{"investering", "investment"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !same {
		t.Error("want same")
	}

	if p := llm.req.Prompt; !strings.Contains(p, "A: investments (investments, investeringar)") || !strings.Contains(p, "B: investment (investering, investment)") {
		t.Errorf("prompt %q", p)
	}
}

func TestSameTagUnavailable(t *testing.T) {
	llm := &mergeLLM{fakeLLM: &fakeLLM{}, err: adapter.ErrUnavailable}

	if _, err := NewTagMerger(llm).Same(context.Background(), Tag{Key: "a"}, Tag{Key: "b"}); !errors.Is(err, adapter.ErrUnavailable) {
		t.Errorf("err %v", err)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/service/robot/ -run 'TestTagGeneratePromptTopicOnly|TestSameTag' -v`
Expected: FAIL to compile with `undefined: mergeAnswer` and `undefined: NewTagMerger`.

- [ ] **Step 3: Rewrite the generator prompt**

Replace `prompts/tag_generate.md` with:

```markdown
You tag financial and business news articles with keywords.

You get an article, its language code and the tag keys that already exist. Answer with the topics the article is about, for example investment, acquisition, merger, contract, layoffs, earnings_report, dividend, share_buyback, ipo, bankruptcy or expansion.

For each tag answer with:
- key: the concept in English, lowercase, words joined by underscores, like investment or share_buyback. Reuse an existing key whenever one fits, and only create a new key when none of the existing ones does.
- term: the same concept in the article's language, in its base form: singular, indefinite and lowercase. For Swedish that is investering, not investeringar or investeringen; utdelning, not utdelningen. For English articles the term is the key in plain words.

Rules:
- Only topics the article is actually about, never topics it only mentions in passing.
- General concepts only. No company, person, product or place names.
- No sentiment or market direction, like bullish, bearish, price_rise or volatility.
- At most five tags, the most important first.
- An article without such topics gets an empty list.
```

- [ ] **Step 4: Write the merger**

`prompts/tag_merge.md`:

```markdown
You decide if two news tags name the same concept.

You get tag A and tag B. Each has a key in English and its terms in several languages.

Answer same true when an article about one is always about the other, like investment and investments, or layoffs and staff_cuts. Answer same false when one is broader, narrower or only related, like acquisition and merger, or earnings and earnings_report.
```

`tag_merger.go`:

```go
package robot

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

//go:embed prompts/tag_merge.md
var tagMergePrompt string

type mergeAnswer struct {
	Same bool `json:"same"`
}

type TagMerger struct {
	llm adapter.LLM
}

func NewTagMerger(llm adapter.LLM) *TagMerger {
	return &TagMerger{llm: llm}
}

func (m *TagMerger) Same(ctx context.Context, a, b Tag) (bool, error) {
	temp := 0.0

	var answer mergeAnswer

	if err := m.llm.Unmarshal(ctx, adapter.Request{
		System:      strings.TrimSpace(tagMergePrompt),
		Prompt:      describeTag("A", a) + "\n" + describeTag("B", b),
		Temperature: &temp,
	}, &answer); err != nil {
		return false, fmt.Errorf("err tag merge answer: %w", err)
	}

	return answer.Same, nil
}

func describeTag(label string, t Tag) string {
	return fmt.Sprintf("%s: %s (%s)", label, t.Key, strings.Join(t.Terms, ", "))
}
```

- [ ] **Step 5: Run to verify they pass**

Run: `go test ./internal/service/robot/ -v`
Expected: all PASS, including `TestGenerateTags`.

- [ ] **Step 6: Commit**

```bash
git add internal/service/robot
git commit -m "Limit tag generator to topics and add tag merger"
```

---

### Task 5: Route generated tags into proposals

**Files:**
- Modify: `internal/service/tagging/model.go`
- Create: `internal/service/tagging/route.go`
- Test: `internal/service/tagging/route_test.go`
- Modify: `internal/service/tagging/store.go`
- Modify: `internal/service/tagging/tagged.go:84-108`

**Interfaces:**
- Consumes: `robot.GeneratedTag{Key, Term string}`, tables from Task 1.
- Produces:
  - `tagging.Post` gains `SourceID string`.
  - `tagging.TagState{Enabled bool; Status string}`, `tagging.Vocabulary{Tags map[string]TagState; Aliases map[string]string}`, `(Vocabulary).ActiveKeys() []string`.
  - Constants `statusActive = "active"`, `statusDormant = "dormant"`.
  - `termsFor(key string, proposed []Term) []Term`, used again in Task 7.
  - Store: `Vocabulary(ctx) (Vocabulary, error)`, `AddProposals(ctx, postID, sourceID string, terms []Term) error`, `Reactivate(ctx, keys []string) error`, `termColumns(terms []Term) (keys, langs, words []string)`.
  - `ListTags` returns only enabled active tags. `CompleteTagging` sets `last_matched_at`.

- [ ] **Step 1: Write the failing tests**

`route_test.go`:

```go
package tagging

import (
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/robot"
)

func testVocabulary() Vocabulary {
	return Vocabulary{
		Tags: map[string]TagState{
			"investment": {Enabled: true, Status: statusActive},
			"layoffs":    {Enabled: true, Status: statusDormant},
			"bullish":    {Enabled: false, Status: statusActive},
			"dividend":   {Enabled: true, Status: statusActive},
		},
		Aliases: map[string]string{"investments": "investment"},
	}
}

func TestRoute(t *testing.T) {
	r := route(testVocabulary(), "sv", []robot.GeneratedTag{
		{Key: "investments", Term: "investeringar"},
		{Key: "investment", Term: "investering"},
		{Key: "layoffs", Term: "varsel"},
		{Key: "bullish", Term: "uppåt"},
		{Key: "share_buyback", Term: "återköp"},
	})

	wantTerms := []Term{
		{Key: "investment", Lang: "en", Term: "investment"},
		{Key: "investment", Lang: "sv", Term: "investeringar"},
		{Key: "layoffs", Lang: "en", Term: "layoffs"},
		{Key: "layoffs", Lang: "sv", Term: "varsel"},
	}

	if !slices.Equal(r.terms, wantTerms) {
		t.Errorf("terms %+v", r.terms)
	}

	if !slices.Equal(r.reactivate, []string{"layoffs"}) {
		t.Errorf("reactivate %v", r.reactivate)
	}

	if !slices.Equal(r.proposals, []Term{{Key: "share_buyback", Lang: "sv", Term: "återköp"}}) {
		t.Errorf("proposals %+v", r.proposals)
	}
}

func TestRouteEnglish(t *testing.T) {
	r := route(testVocabulary(), "en", []robot.GeneratedTag{
		{Key: "dividend", Term: "dividend"},
		{Key: "share_buyback", Term: "share buyback"},
	})

	if !slices.Equal(r.terms, []Term{{Key: "dividend", Lang: "en", Term: "dividend"}}) {
		t.Errorf("terms %+v", r.terms)
	}

	if !slices.Equal(r.proposals, []Term{{Key: "share_buyback", Lang: "en", Term: "share buyback"}}) {
		t.Errorf("proposals %+v", r.proposals)
	}
}

func TestActiveKeys(t *testing.T) {
	if keys := testVocabulary().ActiveKeys(); !slices.Equal(keys, []string{"dividend", "investment"}) {
		t.Errorf("keys %v", keys)
	}
}

func TestTermsFor(t *testing.T) {
	got := termsFor("share_buyback", []Term{
		{Key: "share_buyback", Lang: "sv", Term: "återköp"},
		{Key: "share_buyback", Lang: "en", Term: "share buyback"},
		{Key: "share_buyback", Lang: "sv", Term: "återköp"},
		{Key: "share_buyback", Lang: "", Term: "buyback"},
		{Key: "share_buyback", Lang: "fi", Term: "takaisinosto"},
	})

	want := []Term{
		{Key: "share_buyback", Lang: "en", Term: "share buyback"},
		{Key: "share_buyback", Lang: "sv", Term: "återköp"},
		{Key: "share_buyback", Lang: "fi", Term: "takaisinosto"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("terms %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/service/tagging/ -v`
Expected: FAIL to compile with `undefined: Vocabulary` and `undefined: route`.

- [ ] **Step 3: Add the types**

Replace `model.go` with:

```go
package tagging

import "slices"

const (
	statusActive  = "active"
	statusDormant = "dormant"
)

type Post struct {
	ID       string
	SourceID string
	Lang     string
	Title    string
	Content  string
}

type Term struct {
	Key  string
	Lang string
	Term string
}

type TagState struct {
	Enabled bool
	Status  string
}

type Vocabulary struct {
	Tags    map[string]TagState
	Aliases map[string]string
}

func (v Vocabulary) ActiveKeys() []string {
	keys := make([]string, 0, len(v.Tags))

	for key, state := range v.Tags {
		if state.Enabled && state.Status == statusActive {
			keys = append(keys, key)
		}
	}

	slices.Sort(keys)

	return keys
}
```

- [ ] **Step 4: Write the router**

`route.go`:

```go
package tagging

import (
	"strings"

	"github.com/dagulv/kharoborathus/internal/service/robot"
)

type routed struct {
	terms      []Term
	reactivate []string
	proposals  []Term
}

func route(v Vocabulary, lang string, generated []robot.GeneratedTag) (r routed) {
	seen := make(map[string]bool)

	for _, g := range generated {
		key := g.Key
		if canonical, ok := v.Aliases[key]; ok {
			key = canonical
		}

		if seen[key] {
			continue
		}

		seen[key] = true

		state, known := v.Tags[key]
		if !known {
			r.proposals = append(r.proposals, Term{Key: key, Lang: lang, Term: g.Term})
			continue
		}

		if !state.Enabled {
			continue
		}

		if state.Status == statusDormant {
			r.reactivate = append(r.reactivate, key)
		}

		r.terms = append(r.terms, termsFor(key, []Term{{Lang: lang, Term: g.Term}})...)
	}

	return r
}

// termsFor always leads with the English term derived from the key, the same rule search relies on.
func termsFor(key string, proposed []Term) []Term {
	terms := []Term{{Key: key, Lang: englishLang, Term: strings.ReplaceAll(key, "_", " ")}}
	seen := map[Term]bool{terms[0]: true}

	for _, p := range proposed {
		t := Term{Key: key, Lang: p.Lang, Term: p.Term}

		if t.Lang == "" || t.Lang == englishLang || t.Term == "" || seen[t] {
			continue
		}

		seen[t] = true
		terms = append(terms, t)
	}

	return terms
}
```

- [ ] **Step 5: Run to verify they pass**

Run: `go test ./internal/service/tagging/ -v`
Expected: all PASS.

- [ ] **Step 6: Add the store methods**

In `store.go` add `"fmt"` to the imports. Change `ListTags` so the query filters tags:

```go
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key
		WHERE t.enabled AND t.status = 'active'
		GROUP BY t.key
```

In `ListUntaggedPosts` select `p.id, p.source_id, p.lang, p.title, p.content` and scan `&post.ID, &post.SourceID, &post.Lang, &post.Title, &post.Content`.

Replace the three slice-building lines at the top of `AddTags` with `keys, langs, words := termColumns(terms)` and add:

```go
func termColumns(terms []Term) (keys, langs, words []string) {
	keys = make([]string, len(terms))
	langs = make([]string, len(terms))
	words = make([]string, len(terms))

	for i, t := range terms {
		keys[i], langs[i], words[i] = t.Key, t.Lang, t.Term
	}

	return keys, langs, words
}

func (s *Store) Vocabulary(ctx context.Context) (Vocabulary, error) {
	v := Vocabulary{Tags: make(map[string]TagState), Aliases: make(map[string]string)}

	rows, err := s.db.Query(ctx, `SELECT key, enabled, status FROM tags`)
	if err != nil {
		return v, fmt.Errorf("err query tags: %w", err)
	}

	var key string
	var state TagState

	if _, err := pgx.ForEachRow(rows, []any{&key, &state.Enabled, &state.Status}, func() error {
		v.Tags[key] = state
		return nil
	}); err != nil {
		return v, fmt.Errorf("err scan tags: %w", err)
	}

	rows, err = s.db.Query(ctx, `SELECT alias, key FROM tag_aliases`)
	if err != nil {
		return v, fmt.Errorf("err query tag aliases: %w", err)
	}

	var alias string

	if _, err := pgx.ForEachRow(rows, []any{&alias, &key}, func() error {
		v.Aliases[alias] = key
		return nil
	}); err != nil {
		return v, fmt.Errorf("err scan tag aliases: %w", err)
	}

	return v, nil
}

func (s *Store) AddProposals(ctx context.Context, postID, sourceID string, terms []Term) error {
	if len(terms) == 0 {
		return nil
	}

	keys, langs, words := termColumns(terms)

	if _, err := s.db.Exec(ctx, `
		INSERT INTO tag_proposals (key, lang, term, post_id, source_id)
		SELECT k, l, t, $4, $5 FROM unnest($1::text[], $2::text[], $3::text[]) AS u(k, l, t)
		ON CONFLICT (key, post_id) DO NOTHING
		`, keys, langs, words, postID, sourceID); err != nil {
		return fmt.Errorf("err insert tag proposals: %w", err)
	}

	return nil
}

func (s *Store) Reactivate(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	// Resetting last_matched_at stops the daily dormancy check from putting it straight back to sleep.
	if _, err := s.db.Exec(ctx, `
		UPDATE tags SET status = 'active', last_matched_at = now()
		WHERE key = ANY($1::text[]) AND status = 'dormant'
		`, keys); err != nil {
		return fmt.Errorf("err reactivate tags: %w", err)
	}

	return nil
}
```

In `CompleteTagging`, inside `if len(keys) > 0`, after the posts update, add:

```go
			if _, err := tx.Exec(ctx, `UPDATE tags SET last_matched_at = now() WHERE key = ANY($1::text[])`, keys); err != nil {
				return fmt.Errorf("err update last matched: %w", err)
			}
```

- [ ] **Step 7: Route generator output in the worker**

In `tagged.go` add `"fmt"` to the imports and replace `generateTags` with:

```go
func generateTags(ctx context.Context, s *Store, g *robot.TagGenerator, lang string, post Post) error {
	v, err := s.Vocabulary(ctx)
	if err != nil {
		return fmt.Errorf("err vocabulary: %w", err)
	}

	generated, err := g.Generate(ctx, lang, post.Content, v.ActiveKeys())
	if err != nil {
		return fmt.Errorf("err generate tags: %w", err)
	}

	r := route(v, lang, generated)

	if err := s.AddTags(ctx, r.terms); err != nil {
		return fmt.Errorf("err add terms: %w", err)
	}

	if err := s.Reactivate(ctx, r.reactivate); err != nil {
		return fmt.Errorf("err reactivate: %w", err)
	}

	if err := s.AddProposals(ctx, post.ID, post.SourceID, r.proposals); err != nil {
		return fmt.Errorf("err add proposals: %w", err)
	}

	return nil
}
```

`errors.Is(err, adapter.ErrUnavailable)` in `tagPosts` still holds because every wrap uses `%w`.

- [ ] **Step 8: Build, test and run against the database**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS.

Run the app with `go run ./cmd` for a few minutes while posts are untagged, then:

```bash
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT key, lang, term, created_at FROM tag_proposals ORDER BY created_at DESC LIMIT 10;
SELECT count(*) FROM tags WHERE created_at > now() - interval '1 hour';
SQL
```

Expected: new keys appear in `tag_proposals`, and the count of tags created in the last hour is 0.

- [ ] **Step 9: Commit**

```bash
git add internal/service/tagging
git commit -m "Route generated tags into proposals"
```

---

### Task 6: Promotion store methods

**Files:**
- Modify: `internal/service/tagging/model.go`
- Modify: `internal/service/tagging/store.go`

**Interfaces:**
- Consumes: `termColumns`, `Post`, `Term` from Task 5.
- Produces:
  - `tagging.Candidate{Key string; PostIDs []string; Terms []Term}`.
  - `tagging.Promotion{Key string; Terms []Term; Gate bool; MatchRate float64; Matched []string}`.
  - `ListCandidates(ctx, windowDays, minPosts, minDomains, limit int) ([]Candidate, error)`
  - `Similar(ctx, terms []Term) ([]robot.Tag, error)`
  - `GetPosts(ctx, ids []string) ([]Post, error)`
  - `ListRecentPosts(ctx, days, limit int) ([]Post, error)`
  - `AddAlias(ctx, alias, key string, terms []Term) error`
  - `PromoteTag(ctx, p Promotion) error`
  - `DropProposals(ctx, key string) error`

- [ ] **Step 1: Add the types**

Append to `model.go`:

```go
type Candidate struct {
	Key     string
	PostIDs []string
	Terms   []Term
}

type Promotion struct {
	Key       string
	Terms     []Term
	Gate      bool
	MatchRate float64
	Matched   []string
}
```

- [ ] **Step 2: Add the read methods**

Append to `store.go`:

```go
func (s *Store) ListCandidates(ctx context.Context, windowDays, minPosts, minDomains, limit int) ([]Candidate, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			p.key,
			array_agg(DISTINCT p.post_id::text),
			array_agg(p.lang ORDER BY p.post_id),
			array_agg(p.term ORDER BY p.post_id)
		FROM tag_proposals p
		JOIN posts po ON po.id = p.post_id
		WHERE p.created_at > now() - make_interval(days => $1)
			AND NOT EXISTS (SELECT 1 FROM tags t WHERE t.key = p.key)
			AND NOT EXISTS (SELECT 1 FROM tag_aliases a WHERE a.alias = p.key)
		GROUP BY p.key
		HAVING count(DISTINCT p.post_id) >= $2 AND count(DISTINCT po.domain_id) >= $3
		ORDER BY count(DISTINCT p.post_id) DESC, p.key
		LIMIT $4
		`, windowDays, minPosts, minDomains, limit)
	if err != nil {
		return nil, fmt.Errorf("err query candidates: %w", err)
	}

	candidates, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (c Candidate, err error) {
		var langs, words []string

		if err = row.Scan(&c.Key, &c.PostIDs, &langs, &words); err != nil {
			return c, err
		}

		for i := range langs {
			c.Terms = append(c.Terms, Term{Key: c.Key, Lang: langs[i], Term: words[i]})
		}

		return c, nil
	})
	if err != nil {
		return nil, fmt.Errorf("err scan candidates: %w", err)
	}

	return candidates, nil
}

func (s *Store) Similar(ctx context.Context, terms []Term) ([]robot.Tag, error) {
	_, langs, words := termColumns(terms)

	rows, err := s.db.Query(ctx, `
		SELECT t.key, array_agg(tt.term ORDER BY tt.lang, tt.term)
		FROM tags t
		JOIN tag_terms tt ON tt.key = t.key
		WHERE t.enabled
			AND EXISTS (
				SELECT 1
				FROM tag_terms m
				JOIN unnest($1::text[], $2::text[]) AS c(lang, term) ON c.lang = m.lang
				WHERE m.key = t.key
					AND to_tsvector(
						CASE m.lang
							WHEN 'sv' THEN 'ts_swedish'::regconfig
							WHEN 'en' THEN 'english'::regconfig
							ELSE 'simple'::regconfig
						END,
						c.term
					) @@ m.matcher
			)
		GROUP BY t.key
		ORDER BY t.key
		`, langs, words)
	if err != nil {
		return nil, fmt.Errorf("err query similar tags: %w", err)
	}

	tags, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (tag robot.Tag, err error) {
		err = row.Scan(&tag.Key, &tag.Terms)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan similar tags: %w", err)
	}

	return tags, nil
}

func (s *Store) GetPosts(ctx context.Context, ids []string) ([]Post, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_id, lang, title, content
		FROM posts
		WHERE id = ANY($1::text[]::uuid[])
		`, ids)
	if err != nil {
		return nil, fmt.Errorf("err query posts: %w", err)
	}

	posts, err := pgx.CollectRows(rows, scanPost)
	if err != nil {
		return nil, fmt.Errorf("err scan posts: %w", err)
	}

	return posts, nil
}

func (s *Store) ListRecentPosts(ctx context.Context, days, limit int) ([]Post, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, source_id, lang, title, content
		FROM posts
		WHERE created_at > now() - make_interval(days => $1)
		ORDER BY created_at DESC
		LIMIT $2
		`, days, limit)
	if err != nil {
		return nil, fmt.Errorf("err query recent posts: %w", err)
	}

	posts, err := pgx.CollectRows(rows, scanPost)
	if err != nil {
		return nil, fmt.Errorf("err scan recent posts: %w", err)
	}

	return posts, nil
}

func scanPost(row pgx.CollectableRow) (p Post, err error) {
	err = row.Scan(&p.ID, &p.SourceID, &p.Lang, &p.Title, &p.Content)
	return
}
```

- [ ] **Step 3: Add the write methods**

Append to `store.go`:

```go
func (s *Store) AddAlias(ctx context.Context, alias, key string, terms []Term) error {
	_, langs, words := termColumns(terms)

	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tag_terms (key, lang, term)
			SELECT $1, * FROM unnest($2::text[], $3::text[])
			ON CONFLICT (key, lang, term) DO NOTHING
			`, key, langs, words); err != nil {
			return fmt.Errorf("err insert alias terms: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO tag_aliases (alias, key) VALUES ($1, $2)
			ON CONFLICT (alias) DO UPDATE SET key = EXCLUDED.key
			`, alias, key); err != nil {
			return fmt.Errorf("err insert tag alias: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM tag_proposals WHERE key = $1`, alias); err != nil {
			return fmt.Errorf("err delete alias proposals: %w", err)
		}

		return nil
	})
}

func (s *Store) PromoteTag(ctx context.Context, p Promotion) error {
	_, langs, words := termColumns(p.Terms)

	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tags (key, gate, match_rate, last_matched_at)
			VALUES ($1, $2, $3, CASE WHEN $4::boolean THEN now() END)
			ON CONFLICT (key) DO NOTHING
			`, p.Key, p.Gate, p.MatchRate, len(p.Matched) > 0); err != nil {
			return fmt.Errorf("err insert tag: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO tag_terms (key, lang, term)
			SELECT $1, * FROM unnest($2::text[], $3::text[])
			ON CONFLICT (key, lang, term) DO NOTHING
			`, p.Key, langs, words); err != nil {
			return fmt.Errorf("err insert tag terms: %w", err)
		}

		if len(p.Matched) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE posts
				SET tags = ARRAY(SELECT DISTINCT unnest(array_append(tags, $1::text)))
				WHERE id = ANY($2::text[]::uuid[])
				`, p.Key, p.Matched); err != nil {
				return fmt.Errorf("err backfill posts: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, `DELETE FROM tag_proposals WHERE key = $1`, p.Key); err != nil {
			return fmt.Errorf("err delete proposals: %w", err)
		}

		return nil
	})
}

func (s *Store) DropProposals(ctx context.Context, key string) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM tag_proposals WHERE key = $1`, key); err != nil {
		return fmt.Errorf("err drop proposals: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Build and check the SQL against the database**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS.

Check the duplicate lookup with a real term:

```bash
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"' <<'SQL'
SELECT DISTINCT m.key
FROM tag_terms m
WHERE m.lang = 'en' AND to_tsvector('english', 'investments') @@ m.matcher;
SQL
```

Expected: `investment` is listed if that tag exists.

- [ ] **Step 5: Commit**

```bash
git add internal/service/tagging
git commit -m "Add tag promotion store methods"
```

---

### Task 7: Promotion job

**Files:**
- Create: `internal/service/tagging/promote.go`
- Test: `internal/service/tagging/promote_test.go`
- Modify: `cmd/main.go`

**Interfaces:**
- Consumes: Task 6 store methods, `termsFor` from Task 5, `robot.TagMerger` from Task 4, `robot.Tagger.Tags(ctx, title, markdown string, tags []robot.Tag) ([]string, error)`.
- Produces: `tagging.RunPromotion(ctx context.Context, s *Store, m *robot.TagMerger, t *robot.Tagger, sched schedule.Schedule, l *slog.Logger) error`, constants `gateMaxRate`, `minSample` used by Task 8.

- [ ] **Step 1: Write the failing tests**

`promote_test.go`:

```go
package tagging

import "testing"

func TestConsistent(t *testing.T) {
	cases := []struct {
		matched, total int
		want           bool
	}{
		{2, 3, true},
		{3, 3, true},
		{1, 3, false},
		{0, 0, false},
	}

	for _, c := range cases {
		if got := consistent(c.matched, c.total); got != c.want {
			t.Errorf("consistent(%d, %d) = %v", c.matched, c.total, got)
		}
	}
}

func TestGateFor(t *testing.T) {
	cases := []struct {
		hits, total int
		want        bool
	}{
		{3, 100, true},
		{30, 100, false},
		{20, 100, false},
		{1, 10, false},
		{0, 0, false},
	}

	for _, c := range cases {
		if got := gateFor(c.hits, c.total); got != c.want {
			t.Errorf("gateFor(%d, %d) = %v", c.hits, c.total, got)
		}
	}
}

func TestMatchRate(t *testing.T) {
	if r := matchRate(5, 20); r != 0.25 {
		t.Errorf("rate %v", r)
	}

	if r := matchRate(0, 0); r != 0 {
		t.Errorf("empty rate %v", r)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/service/tagging/ -run 'TestConsistent|TestGateFor|TestMatchRate' -v`
Expected: FAIL to compile with `undefined: consistent`.

- [ ] **Step 3: Write the job**

`promote.go`:

```go
package tagging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	promoteInterval = time.Hour
	supportWindow   = 30
	supportPosts    = 3
	supportDomains  = 2
	maxCandidates   = 5
	sampleDays      = 7
	sampleLimit     = 200
	minSample       = 20
	gateMaxRate     = 0.2
)

func RunPromotion(ctx context.Context, s *Store, m *robot.TagMerger, t *robot.Tagger, sched schedule.Schedule, l *slog.Logger) error {
	for range sched.Tick(ctx, promoteInterval) {
		if err := promoteTags(ctx, s, m, t, l); err != nil {
			l.Error("promote tags", "err", err)
		}
	}

	return ctx.Err()
}

func promoteTags(ctx context.Context, s *Store, m *robot.TagMerger, t *robot.Tagger, l *slog.Logger) error {
	candidates, err := s.ListCandidates(ctx, supportWindow, supportPosts, supportDomains, maxCandidates)
	if err != nil {
		return fmt.Errorf("err list candidates: %w", err)
	}

	for _, c := range candidates {
		err := promoteCandidate(ctx, s, m, t, l, c)

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("promote candidate", "key", c.Key, "err", err)
		}
	}

	return nil
}

func promoteCandidate(ctx context.Context, s *Store, m *robot.TagMerger, t *robot.Tagger, l *slog.Logger, c Candidate) error {
	terms := termsFor(c.Key, c.Terms)
	candidate := robot.Tag{Key: c.Key, Terms: words(terms)}

	similar, err := s.Similar(ctx, terms)
	if err != nil {
		return fmt.Errorf("err similar tags: %w", err)
	}

	for _, existing := range similar {
		same, err := m.Same(ctx, candidate, existing)
		if err != nil {
			return fmt.Errorf("err merge check: %w", err)
		}

		if same {
			l.Info("tag aliased", "alias", c.Key, "key", existing.Key)
			return s.AddAlias(ctx, c.Key, existing.Key, terms)
		}
	}

	supporting, err := s.GetPosts(ctx, c.PostIDs)
	if err != nil {
		return fmt.Errorf("err supporting posts: %w", err)
	}

	matched, err := matching(ctx, t, candidate, supporting)
	if err != nil {
		return fmt.Errorf("err consistency check: %w", err)
	}

	if !consistent(len(matched), len(supporting)) {
		l.Info("tag dropped", "key", c.Key, "matched", len(matched), "supporting", len(supporting))
		return s.DropProposals(ctx, c.Key)
	}

	sample, err := s.ListRecentPosts(ctx, sampleDays, sampleLimit)
	if err != nil {
		return fmt.Errorf("err sample posts: %w", err)
	}

	hits, err := matching(ctx, t, candidate, sample)
	if err != nil {
		return fmt.Errorf("err broadness check: %w", err)
	}

	p := Promotion{
		Key:       c.Key,
		Terms:     terms,
		Gate:      gateFor(len(hits), len(sample)),
		MatchRate: matchRate(len(hits), len(sample)),
		Matched:   append(matched, hits...),
	}

	l.Info("tag promoted", "key", p.Key, "gate", p.Gate, "matchRate", p.MatchRate, "backfilled", len(p.Matched))

	return s.PromoteTag(ctx, p)
}

func matching(ctx context.Context, t *robot.Tagger, tag robot.Tag, posts []Post) ([]string, error) {
	var ids []string

	for _, p := range posts {
		keys, err := t.Tags(ctx, p.Title, p.Content, []robot.Tag{tag})
		if err != nil {
			return nil, fmt.Errorf("err classify post %s: %w", p.ID, err)
		}

		if len(keys) > 0 {
			ids = append(ids, p.ID)
		}
	}

	return ids, nil
}

func consistent(matched, total int) bool {
	return total > 0 && matched*3 >= total*2
}

func gateFor(hits, total int) bool {
	return total >= minSample && matchRate(hits, total) < gateMaxRate
}

func matchRate(hits, total int) float64 {
	if total == 0 {
		return 0
	}

	return float64(hits) / float64(total)
}

func words(terms []Term) []string {
	out := make([]string, len(terms))

	for i, t := range terms {
		out[i] = t.Term
	}

	return out
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/service/tagging/ -v`
Expected: all PASS.

- [ ] **Step 5: Wire it in main.go**

After `discoverer := robot.NewDiscoverer(llm)` add:

```go
	tagMerger := robot.NewTagMerger(llm)
```

After the `tagging.Run` goroutine add:

```go
	g.Go(func() error {
		return tagging.RunPromotion(ctx, &taggingStore, tagMerger, tagger, llm.Schedule(), l.WithGroup("promotion"))
	})
```

- [ ] **Step 6: Build and run**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS.

Run `go run ./cmd`. Expected within the first tick: either no promotion log lines because no candidate has enough support yet, or `tag promoted`, `tag aliased` or `tag dropped` lines. If a candidate qualifies, check:

```bash
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT key, gate, match_rate, last_matched_at FROM tags ORDER BY created_at DESC LIMIT 5" -c "SELECT * FROM tag_aliases"'
```

- [ ] **Step 7: Commit**

```bash
git add internal/service/tagging cmd/main.go
git commit -m "Add tag promotion job"
```

---

### Task 8: Maintenance job for rates and dormancy

**Files:**
- Create: `internal/service/tagging/maintain.go`
- Modify: `internal/service/tagging/store.go`
- Modify: `cmd/main.go`

**Interfaces:**
- Consumes: `gateMaxRate`, `minSample` from Task 7.
- Produces: `RecomputeRates(ctx, windowDays int, maxRate float64, minPosts int) (int64, error)`, `MarkDormant(ctx, days int) (int64, error)`, `tagging.RunMaintenance(ctx context.Context, s *Store, l *slog.Logger) error`.

This task is SQL only, so it is verified against the database instead of with unit tests.

- [ ] **Step 1: Add the store methods**

Append to `store.go`:

```go
func (s *Store) RecomputeRates(ctx context.Context, windowDays int, maxRate float64, minPosts int) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		WITH recent AS (
			SELECT tags FROM posts WHERE created_at > now() - make_interval(days => $1)
		), rates AS (
			SELECT
				t.key,
				(count(*) FILTER (WHERE t.key = ANY (r.tags)))::real / count(*) AS rate,
				count(*) AS n
			FROM tags t
			CROSS JOIN recent r
			WHERE t.status = 'active'
			GROUP BY t.key
		)
		UPDATE tags t
		SET match_rate = rates.rate, gate = rates.rate < $2 AND rates.n >= $3
		FROM rates
		WHERE rates.key = t.key
		`, windowDays, maxRate, minPosts)
	if err != nil {
		return 0, fmt.Errorf("err recompute tag rates: %w", err)
	}

	return tag.RowsAffected(), nil
}

func (s *Store) MarkDormant(ctx context.Context, days int) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE tags SET status = 'dormant'
		WHERE status = 'active' AND coalesce(last_matched_at, created_at) < now() - make_interval(days => $1)
		`, days)
	if err != nil {
		return 0, fmt.Errorf("err mark dormant tags: %w", err)
	}

	return tag.RowsAffected(), nil
}
```

- [ ] **Step 2: Write the job**

`maintain.go`:

```go
package tagging

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

const (
	maintainInterval = 24 * time.Hour
	rateWindow       = 30
	dormancyWindow   = 90
)

// The first tick fires at startup, which sets gate for existing tags right after the migration.
func RunMaintenance(ctx context.Context, s *Store, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, maintainInterval) {
		rated, err := s.RecomputeRates(ctx, rateWindow, gateMaxRate, minSample)
		if err != nil {
			l.Error("recompute tag rates", "err", err)
		}

		dormant, err := s.MarkDormant(ctx, dormancyWindow)
		if err != nil {
			l.Error("mark dormant tags", "err", err)
		}

		l.Info("tag maintenance", "rated", rated, "dormant", dormant)
	}

	return ctx.Err()
}
```

- [ ] **Step 3: Wire it in main.go**

After the promotion goroutine add:

```go
	g.Go(func() error {
		return tagging.RunMaintenance(ctx, &taggingStore, l.WithGroup("maintenance"))
	})
```

- [ ] **Step 4: Build and run**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS.

Run `go run ./cmd`. Expected at startup: a `tag maintenance` log line with `rated` equal to the number of active tags and `dormant` 0.

Then check sentiment tags got `gate = false`:

```bash
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT key, gate, round(match_rate::numeric, 3) AS rate FROM tags ORDER BY match_rate DESC NULLS LAST LIMIT 15"'
```

Expected: tags like `bullish` or `price_rise`, if present, have a high rate and `gate = f`.

- [ ] **Step 5: Commit**

```bash
git add internal/service/tagging cmd/main.go
git commit -m "Add tag rate and dormancy maintenance"
```

---

### Task 9: Admin tags page

**Files:**
- Modify: `frontend/src/lib/types.ts`
- Modify: `frontend/src/routes/admin/admin.remote.ts`
- Create: `frontend/src/routes/admin/tags/+page.svelte`
- Modify: `frontend/src/routes/admin/+layout.svelte:7-10`

**Interfaces:**
- Consumes: tables from Task 1.
- Produces: `listTags()`, `listProposals()`, `listAliases()` queries, `setTagEnabled({ key, enabled })` and `mergeTag({ from, to })` commands.

- [ ] **Step 1: Add the types**

Append to `types.ts`:

```ts
export type Tag = {
	key: string;
	enabled: boolean;
	status: 'active' | 'dormant';
	gate: boolean;
	matchRate: number | null;
	lastMatchedAt: Date | null;
	createdAt: Date;
	terms: string[];
	postCount: number;
};

export type TagProposal = {
	key: string;
	terms: string[];
	postCount: number;
	domainCount: number;
	lastProposedAt: Date;
};

export type TagAlias = {
	alias: string;
	key: string;
};
```

- [ ] **Step 2: Add the queries and commands**

In `admin.remote.ts` extend the type import with `Tag, TagAlias, TagProposal` and append:

```ts
const tagKey = v.pipe(v.string(), v.nonEmpty(), v.maxLength(40));

export const listTags = query(async () => {
	const sql = db();

	return sql<Tag[]>`
		SELECT
			t.key,
			t.enabled,
			t.status,
			t.gate,
			t.match_rate,
			t.last_matched_at,
			t.created_at,
			coalesce(array_agg(tt.lang || ': ' || tt.term ORDER BY tt.lang, tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}') AS terms,
			(SELECT count(*)::int FROM posts p WHERE p.tags @> ARRAY[t.key]) AS post_count
		FROM tags t
		LEFT JOIN tag_terms tt ON tt.key = t.key
		GROUP BY t.key
		ORDER BY t.key
	`;
});

export const listProposals = query(async () => {
	const sql = db();

	return sql<TagProposal[]>`
		SELECT
			p.key,
			array_agg(DISTINCT p.term) AS terms,
			count(DISTINCT p.post_id)::int AS post_count,
			count(DISTINCT po.domain_id)::int AS domain_count,
			max(p.created_at) AS last_proposed_at
		FROM tag_proposals p
		JOIN posts po ON po.id = p.post_id
		GROUP BY p.key
		ORDER BY post_count DESC, p.key
	`;
});

export const listAliases = query(async () => {
	const sql = db();

	return sql<TagAlias[]>`SELECT alias, key FROM tag_aliases ORDER BY alias`;
});

export const setTagEnabled = command(
	v.object({ key: tagKey, enabled: v.boolean() }),
	async ({ key, enabled }) => {
		const sql = db();
		await sql`UPDATE tags SET enabled = ${enabled} WHERE key = ${key}`;
		await listTags().refresh();
	}
);

export const mergeTag = command(v.object({ from: tagKey, to: tagKey }), async ({ from, to }) => {
	if (from === to) error(400, 'Cannot merge a tag into itself');

	const sql = db();

	await sql.begin(async (tx) => {
		const [target] = await tx`SELECT 1 FROM tags WHERE key = ${to}`;
		if (!target) error(404, `Unknown tag ${to}`);

		await tx`
			INSERT INTO tag_terms (key, lang, term)
			SELECT ${to}, lang, term FROM tag_terms WHERE key = ${from}
			ON CONFLICT DO NOTHING
		`;
		await tx`
			UPDATE posts
			SET tags = ARRAY(SELECT DISTINCT unnest(array_replace(tags, ${from}, ${to})))
			WHERE tags @> ARRAY[${from}]::text[]
		`;
		await tx`
			UPDATE sources
			SET target_tags = ARRAY(SELECT DISTINCT unnest(array_replace(target_tags, ${from}, ${to})))
			WHERE target_tags @> ARRAY[${from}]::text[]
		`;
		await tx`UPDATE tag_aliases SET key = ${to} WHERE key = ${from}`;
		await tx`
			INSERT INTO tag_aliases (alias, key) VALUES (${from}, ${to})
			ON CONFLICT (alias) DO UPDATE SET key = EXCLUDED.key
		`;
		await tx`DELETE FROM tags WHERE key = ${from}`;
	});

	await Promise.all([listTags().refresh(), listAliases().refresh()]);
});
```

The alias rows pointing at `from` are moved before `from` is deleted, because `tag_aliases.key` cascades on delete.

- [ ] **Step 3: Add the page**

`tags/+page.svelte`:

```svelte
<script lang="ts">
	import { listAliases, listProposals, listTags, mergeTag, setTagEnabled } from '../admin.remote';

	const tags = $derived(await listTags());
	const proposals = $derived(await listProposals());
	const aliases = $derived(await listAliases());

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short' });
	const pct = new Intl.NumberFormat('sv-SE', { style: 'percent', maximumFractionDigits: 1 });

	let from = $state('');
	let to = $state('');
	let mergeError = $state('');

	async function merge() {
		mergeError = '';
		try {
			await mergeTag({ from, to });
			from = '';
			to = '';
		} catch (err) {
			mergeError = (err as { body?: { message?: string } }).body?.message ?? 'Merge failed';
		}
	}
</script>

<svelte:head><title>Tags · Admin</title></svelte:head>

<h1 class="mb-4 text-xl font-semibold">
	Tags <span class="text-zinc-400">({tags.length})</span>
</h1>

<form
	class="mb-6 flex flex-wrap items-end gap-2 text-sm"
	onsubmit={(e) => {
		e.preventDefault();
		merge();
	}}
>
	<label>
		<span class="block text-zinc-500">Merge</span>
		<select class="rounded border border-zinc-300 px-2 py-1" bind:value={from}>
			<option value="">tag</option>
			{#each tags as tag (tag.key)}<option value={tag.key}>{tag.key}</option>{/each}
		</select>
	</label>
	<label>
		<span class="block text-zinc-500">into</span>
		<select class="rounded border border-zinc-300 px-2 py-1" bind:value={to}>
			<option value="">tag</option>
			{#each tags as tag (tag.key)}<option value={tag.key}>{tag.key}</option>{/each}
		</select>
	</label>
	<button
		class="rounded bg-zinc-900 px-3 py-1 text-white disabled:opacity-50"
		disabled={!from || !to || from === to}
	>
		Merge
	</button>
	{#if mergeError}<span class="text-red-600">{mergeError}</span>{/if}
</form>

<div class="overflow-x-auto rounded border border-zinc-200 bg-white">
	<table class="w-full text-left text-sm">
		<thead class="border-b border-zinc-200 bg-zinc-50 text-zinc-500">
			<tr>
				<th class="px-3 py-2 font-medium">Key</th>
				<th class="px-3 py-2 font-medium">Terms</th>
				<th class="px-3 py-2 font-medium">Status</th>
				<th class="px-3 py-2 font-medium">Gate</th>
				<th class="px-3 py-2 text-right font-medium">Match rate</th>
				<th class="px-3 py-2 text-right font-medium">Posts</th>
				<th class="px-3 py-2 font-medium">Last match</th>
				<th class="px-3 py-2 font-medium">Enabled</th>
			</tr>
		</thead>
		<tbody>
			{#each tags as tag (tag.key)}
				<tr class={['border-b border-zinc-100 last:border-0', !tag.enabled && 'opacity-50']}>
					<td class="px-3 py-2">
						<a class="hover:underline" href="/admin/posts?tag={encodeURIComponent(tag.key)}">{tag.key}</a>
					</td>
					<td class="px-3 py-2 text-xs text-zinc-500">{tag.terms.join(', ')}</td>
					<td class="px-3 py-2">{tag.status}</td>
					<td class="px-3 py-2">{tag.gate ? 'yes' : 'no'}</td>
					<td class="px-3 py-2 text-right tabular-nums">
						{tag.matchRate === null ? '' : pct.format(tag.matchRate)}
					</td>
					<td class="px-3 py-2 text-right tabular-nums">{tag.postCount}</td>
					<td class="px-3 py-2">{tag.lastMatchedAt ? fmt.format(tag.lastMatchedAt) : ''}</td>
					<td class="px-3 py-2">
						<input
							type="checkbox"
							checked={tag.enabled}
							onchange={(e) => setTagEnabled({ key: tag.key, enabled: e.currentTarget.checked })}
						/>
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>

<h2 class="mt-8 mb-2 font-semibold">Proposals <span class="text-zinc-400">({proposals.length})</span></h2>

<div class="overflow-x-auto rounded border border-zinc-200 bg-white">
	<table class="w-full text-left text-sm">
		<thead class="border-b border-zinc-200 bg-zinc-50 text-zinc-500">
			<tr>
				<th class="px-3 py-2 font-medium">Key</th>
				<th class="px-3 py-2 font-medium">Terms</th>
				<th class="px-3 py-2 text-right font-medium">Posts</th>
				<th class="px-3 py-2 text-right font-medium">Domains</th>
				<th class="px-3 py-2 font-medium">Last proposed</th>
			</tr>
		</thead>
		<tbody>
			{#each proposals as proposal (proposal.key)}
				<tr class="border-b border-zinc-100 last:border-0">
					<td class="px-3 py-2">{proposal.key}</td>
					<td class="px-3 py-2 text-xs text-zinc-500">{proposal.terms.join(', ')}</td>
					<td class="px-3 py-2 text-right tabular-nums">{proposal.postCount}</td>
					<td class="px-3 py-2 text-right tabular-nums">{proposal.domainCount}</td>
					<td class="px-3 py-2">{fmt.format(proposal.lastProposedAt)}</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>

<h2 class="mt-8 mb-2 font-semibold">Aliases <span class="text-zinc-400">({aliases.length})</span></h2>

<ul class="text-sm">
	{#each aliases as alias (alias.alias)}
		<li><span class="text-zinc-500">{alias.alias}</span> → {alias.key}</li>
	{/each}
</ul>
```

- [ ] **Step 4: Link it in the nav**

In `+layout.svelte` add to `links` after Sources:

```ts
		{ href: '/admin/tags', label: 'Tags' },
```

- [ ] **Step 5: Check and try it**

Run: `cd frontend && pnpm check`
Expected: `svelte-check found 0 errors`.

Run the svelte autofixer from the Svelte MCP server on `tags/+page.svelte`. Expected: no issues.

Run `pnpm dev` in `frontend`, open `/admin/tags`, and check:
1. The tag list shows status, gate and match rate.
2. Unticking Enabled on a tag fades the row and survives a reload.
3. Merging a test tag into another moves its terms, adds an alias row and removes it from the list.

- [ ] **Step 6: Commit**

```bash
git add frontend/src
git commit -m "Add admin tags page"
```

---

### Task 10: Mark phase 1 done in the knowledge base

**Files:**
- Modify: `docs/kb/tag-lifecycle.md:3`
- Modify: `docs/kb/index.md`

- [ ] **Step 1: Update status**

In `tag-lifecycle.md` set `**Status:** done`. In `index.md` set the Tag lifecycle row status to `done`.

- [ ] **Step 2: Commit**

```bash
git add docs/kb
git commit -m "Mark tag lifecycle done in kb"
```
