# Phase 2: Shadow Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the admin switches, then the shadow gate behind the `shadow_gate` switch. While it is on, every classified post records whether it passed its source's shadow gate. A daily job flags enabled sources with a full window of 50 gated posts and a pass rate below 70%. Admin lists flagged sources so you can keep, retarget or disable them.

**Architecture:** A `switches` table holds one row per guarded feature, and a missing row means off. A small Go package reads a switch, and an admin page toggles it and shows readiness. The tagging worker reads `shadow_gate` once per tick. When it is on, a pure function `shadowGate` decides pass, fail or unknown from the post's source `target_tags`, the tag vocabulary and the matched keys, and `CompleteTagging` stores it in `tag_checked.gate_passed`. Daily maintenance reads the same switch and runs `FlagSources`, which sets `sources.flagged_at` from the last 50 gated posts since the source's last review. The admin sources page shows flagged sources with their pass rate and one review command.

**Tech Stack:** Go 1.27, pgx v5, Postgres 18, SvelteKit 3 remote functions (`command(...).updates(...)`), postgres.js with camel transform, valibot, Svelte 5.

**Spec:** `docs/kb/rollout.md` (switches and phase 2), `docs/kb/classifier-gate.md` (shadow gate section), `docs/kb/trust-model.md`.

## Global Constraints

- Phase 2 ships the switches and the shadow gate only. The hard gate, the article question, `link_queue` and source `status` or `origin` belong to phase 4. Do not add them, and do not add rows for other switches.
- Every post is trusted in phase 2. Never reject or hide a post because of the gate.
- Schema changes go straight into `internal/migrations/schema.sql`. No numbered migration file, because the project has not been deployed yet. This overrides the "Every phase" migration bullet in `rollout.md`, which Task 8 rewrites.
- The `shadow_gate` row is inserted by `schema.sql` with `ON CONFLICT (key) DO NOTHING` and starts off.
- While `shadow_gate` is off, tagging runs as before, no `gate_passed` is stored and nothing is flagged.
- Workers read their switch at the start of every tick. Nothing but you sets a switch, and readiness is shown, never enforced.
- Tag threshold stays the existing `tagThreshold` of 0.8. Nothing in `robot` changes.
- Pass rate window: last 50 posts with a gate result. Pass rate floor: 70%. A source with fewer than 50 gated posts in the window is never flagged.
- A post passes if it gets any of its source's `target_tags`. If `target_tags` is empty, any enabled, active tag with `gate = true` counts.
- Go style: `any` not `interface{}`, guard clauses, wrap leaf errors as `fmt.Errorf("err <action>: %w", err)`, comments only for gotchas.
- Use `pnpm`, never `npx`.
- One commit per task. End each commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.
- The dev database still runs the pre-phase-1 schema. Do not reset it. Verify SQL on a throwaway database built from `schema.sql`, then drop it.

## Plan rulings against the spec

These fill gaps the spec leaves open. Task 8 writes them back into the kb.

- **`sources.reviewed_at` is added.** Without it, keeping or retargeting a flagged source does nothing: the same failing posts stay in the window and the next daily run flags it again. The window only counts posts checked after `reviewed_at`, so a reviewed source needs a fresh full window before it can be flagged again.
- **No basis means no result.** With the switch on, if a post's source has no `target_tags` and no enabled, active gate tag exists, `gate_passed` is NULL, not false.
- **A failed classification stores NULL.** It is not evidence of drift.
- **Target tags are resolved through `tag_aliases`.** Admin accepts free text, and a merge turns keys into aliases.
- **Switch readiness is one text column per key, built in SQL.** Later phases add a `WHEN` branch for their key.

## Review Focus

- With `shadow_gate` off, posts must be tagged as before and store `gate_passed = NULL`, and maintenance must flag nothing. Pinned by code in Task 5 and Task 6, and checked on the throwaway database in Task 6.
- A fresh database with no gate tags and sources without `target_tags` must store NULL, not false. Pinned in Task 4 (`TestShadowGateWithoutGateTags`) and Task 6 (source `h`).
- A target tag typed as an alias, like `investments`, must match posts tagged with the canonical `investment`. Pinned in Task 4.
- A source with fewer than 50 gated posts, including one reviewed recently, must not be flagged however low its rate. Pinned in Task 6 (sources `c` and `d`).
- An already flagged source keeps its original `flagged_at`, and disabled or deleted sources are never flagged. Pinned in Task 6 (sources `f` and `g`).

---

### Task 1: Switches table and admin page

**Files:**
- Modify: `internal/migrations/schema.sql` (append the `switches` table)
- Modify: `frontend/src/lib/types.ts` (append `Switch`)
- Modify: `frontend/src/routes/admin/admin.remote.ts` (append `listSwitches` and `setSwitch`)
- Create: `frontend/src/routes/admin/switches/+page.svelte`
- Modify: `frontend/src/routes/admin/+layout.svelte` (nav link)

**Interfaces:**
- Produces: table `switches (key, enabled, updated_at)`, type `Switch`, `listSwitches()` returning `Switch[]` with a `readiness: string | null` column, `setSwitch({ key, enabled })`. Task 2 adds the first row and its readiness branch.

Edit the `.svelte` files through the `svelte:svelte-file-editor` agent or the Svelte MCP tools.

- [ ] **Step 1: Add the table**

Append to `internal/migrations/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS switches (
  key TEXT PRIMARY KEY,
  enabled BOOLEAN NOT NULL DEFAULT false,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Load the schema into a throwaway database**

Run:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" gate_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d gate_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d gate_check -c "\d switches" -c "SELECT count(*) FROM switches"'
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" gate_check'
```

Expected: every command exits 0. `switches` has `key`, `enabled` defaulting to false and `updated_at`. The count is 0.

- [ ] **Step 3: Add the type**

Append to `frontend/src/lib/types.ts`:

```ts

export type Switch = {
	key: string;
	enabled: boolean;
	updatedAt: Date;
	readiness: string | null;
};
```

- [ ] **Step 4: Add the remote functions**

In `frontend/src/routes/admin/admin.remote.ts`, add `Switch` to the type import:

```ts
import type { Post, PostSummary, Source, Switch, Tag, TagAlias, TagProposal } from '#lib/types.js';
```

Append to the end of the file:

```ts
// Readiness per key mirrors the switch table in docs/kb/rollout.md
export const listSwitches = query(async () => {
	const sql = db();

	return sql<Switch[]>`
		SELECT
			s.key,
			s.enabled,
			s.updated_at,
			NULL::text AS readiness
		FROM switches s
		ORDER BY s.key
	`;
});

export const setSwitch = command(
	v.object({ key: v.pipe(v.string(), v.nonEmpty(), v.maxLength(40)), enabled: v.boolean() }),
	async ({ key, enabled }) => {
		const sql = db();
		const [row] = await sql`
			UPDATE switches SET enabled = ${enabled}, updated_at = now()
			WHERE key = ${key}
			RETURNING 1
		`;

		if (!row) error(404, `Unknown switch ${key}`);

		await listSwitches().refresh();
	}
);
```

No switch exists yet, so readiness is NULL. Task 2 replaces it with a `CASE` per key.

- [ ] **Step 5: Add the page**

`frontend/src/routes/admin/switches/+page.svelte`:

```svelte
<script lang="ts">
	import { listSwitches, setSwitch } from '../admin.remote';

	const switches = $derived(await listSwitches());

	const fmt = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeStyle: 'short' });

	const descriptions: Record<string, string> = {};

	let failed = $state('');

	async function toggle(key: string, enabled: boolean) {
		failed = '';
		try {
			await setSwitch({ key, enabled });
		} catch {
			failed = key;
			await listSwitches().refresh();
		}
	}
</script>

<svelte:head><title>Switches · Admin</title></svelte:head>

<h1 class="mb-1 text-xl font-semibold">Switches</h1>
<p class="mb-4 text-sm text-zinc-500">
	Features that need data before they run. Readiness is a hint, never enforced.
</p>

<div class="overflow-x-auto rounded border border-zinc-200 bg-white">
	<table class="w-full text-left text-sm">
		<thead class="border-b border-zinc-200 bg-zinc-50 text-zinc-500">
			<tr>
				<th class="px-3 py-2 font-medium">Switch</th>
				<th class="px-3 py-2 font-medium">Readiness</th>
				<th class="px-3 py-2 font-medium">Changed</th>
				<th class="px-3 py-2 font-medium">On</th>
			</tr>
		</thead>
		<tbody>
			{#each switches as sw (sw.key)}
				<tr class="border-b border-zinc-100 last:border-0">
					<td class="px-3 py-2">
						<div class="font-mono">{sw.key}</div>
						{#if descriptions[sw.key]}
							<div class="text-xs text-zinc-500">{descriptions[sw.key]}</div>
						{/if}
					</td>
					<td class="px-3 py-2 text-zinc-600">{sw.readiness ?? '—'}</td>
					<td class="px-3 py-2 whitespace-nowrap text-zinc-500">{fmt.format(sw.updatedAt)}</td>
					<td class="px-3 py-2">
						<input
							type="checkbox"
							checked={sw.enabled}
							onchange={(e) => toggle(sw.key, e.currentTarget.checked)}
						/>
						{#if failed === sw.key}<span class="ml-2 text-xs text-red-600">Failed</span>{/if}
					</td>
				</tr>
			{:else}
				<tr><td colspan="4" class="px-3 py-6 text-center text-zinc-500">No switches</td></tr>
			{/each}
		</tbody>
	</table>
</div>
```

- [ ] **Step 6: Add the nav link**

In `frontend/src/routes/admin/+layout.svelte`, replace:

```ts
		{ href: '/admin/tags', label: 'Tags' },
		{ href: '/admin/posts', label: 'Posts' }
```

with:

```ts
		{ href: '/admin/tags', label: 'Tags' },
		{ href: '/admin/posts', label: 'Posts' },
		{ href: '/admin/switches', label: 'Switches' }
```

- [ ] **Step 7: Type check and autofix**

Run: `cd frontend && pnpm check`
Expected: `svelte-check found 0 errors`.

Run the Svelte MCP `svelte-autofixer` on `switches/+page.svelte`. Expected: no issues.

- [ ] **Step 8: Check the admin SQL on a throwaway database**

Run the `createdb` and schema load commands from Step 2 again. Then run:

```bash
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d gate_check' <<'SQL'
SELECT s.key, s.enabled, s.updated_at, NULL::text AS readiness FROM switches s ORDER BY s.key;
INSERT INTO switches (key) VALUES ('probe');
UPDATE switches SET enabled = true, updated_at = now() WHERE key = 'probe' RETURNING key, enabled;
UPDATE switches SET enabled = true, updated_at = now() WHERE key = 'missing' RETURNING 1;
SQL
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" gate_check'
```

Expected: the first select returns 0 rows. The first update returns `probe | t`. The second update returns 0 rows, which `setSwitch` turns into a 404.

- [ ] **Step 9: Commit**

```bash
git add internal/migrations/schema.sql frontend/src/lib/types.ts frontend/src/routes/admin/admin.remote.ts frontend/src/routes/admin/switches/+page.svelte frontend/src/routes/admin/+layout.svelte
git commit -m "Add switches table and admin page

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Shadow gate schema and switch row

**Files:**
- Modify: `internal/migrations/schema.sql` (the `sources` table, the `tag_checked` table, the end of the file)
- Modify: `frontend/src/routes/admin/admin.remote.ts` (`listSwitches` readiness)
- Modify: `frontend/src/routes/admin/switches/+page.svelte` (`descriptions`)

**Interfaces:**
- Consumes: `switches` table and `listSwitches` from Task 1.
- Produces: `tag_checked.gate_passed BOOLEAN` (nullable), `sources.flagged_at TIMESTAMPTZ`, `sources.reviewed_at TIMESTAMPTZ`, switch row `shadow_gate` starting off, readiness text `<n> gate tags`.

- [ ] **Step 1: Edit the `sources` table**

In `internal/migrations/schema.sql`, replace:

```sql
  target_tags TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
```

with:

```sql
  target_tags TEXT[] NOT NULL DEFAULT '{}',
  flagged_at TIMESTAMPTZ,
  reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ
);
```

- [ ] **Step 2: Edit the `tag_checked` table**

Replace:

```sql
CREATE TABLE IF NOT EXISTS tag_checked (
  post_id UUID PRIMARY KEY REFERENCES posts (id) ON DELETE CASCADE,
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

with:

```sql
CREATE TABLE IF NOT EXISTS tag_checked (
  post_id UUID PRIMARY KEY REFERENCES posts (id) ON DELETE CASCADE,
  gate_passed BOOLEAN, -- NULL when the switch was off or there was nothing to gate against
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 3: Insert the switch row**

Append to `internal/migrations/schema.sql`, after the `switches` table:

```sql

INSERT INTO switches (key) VALUES ('shadow_gate') ON CONFLICT (key) DO NOTHING;
```

- [ ] **Step 4: Add readiness and a description**

In `admin.remote.ts`, inside `listSwitches`, replace:

```sql
			NULL::text AS readiness
```

with:

```sql
			CASE s.key
				WHEN 'shadow_gate' THEN (
					SELECT count(*) FROM tags WHERE enabled AND status = 'active' AND gate
				) || ' gate tags'
			END AS readiness
```

In `switches/+page.svelte`, replace:

```ts
	const descriptions: Record<string, string> = {};
```

with:

```ts
	const descriptions: Record<string, string> = {
		shadow_gate:
			'Stores the shadow gate result per post and flags drifting sources. Turn on when several gate tags exist.'
	};
```

- [ ] **Step 5: Check on a throwaway database**

Run:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" gate_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d gate_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d gate_check' <<'SQL'
UPDATE switches SET enabled = true WHERE key = 'shadow_gate';
INSERT INTO switches (key) VALUES ('shadow_gate') ON CONFLICT (key) DO NOTHING;
INSERT INTO tags (key, gate) VALUES ('investment', true), ('earnings', false);
SQL
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d gate_check' <<'SQL'
\d sources
\d tag_checked
SELECT s.key, s.enabled,
	CASE s.key
		WHEN 'shadow_gate' THEN (
			SELECT count(*) FROM tags WHERE enabled AND status = 'active' AND gate
		) || ' gate tags'
	END AS readiness
FROM switches s ORDER BY s.key;
SQL
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" gate_check'
```

Expected: every command exits 0. `sources` lists `flagged_at` and `reviewed_at`, and `tag_checked` lists nullable `gate_passed`. The select returns `shadow_gate | t | 1 gate tags`, so rerunning the insert did not reset your choice.

- [ ] **Step 6: Type check**

Run: `cd frontend && pnpm check`
Expected: `svelte-check found 0 errors`.

- [ ] **Step 7: Commit**

```bash
git add internal/migrations/schema.sql frontend/src/routes/admin/admin.remote.ts frontend/src/routes/admin/switches/+page.svelte
git commit -m "Add shadow gate schema and switch

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Switches Go package

**Files:**
- Create: `internal/service/switches/store.go`

**Interfaces:**
- Consumes: `switches` table and the `shadow_gate` row from Tasks 1 and 2.
- Produces: package `switches` with `const ShadowGate = "shadow_gate"`, `type Store`, `func NewStore(db *database.Database) Store`, `func (s *Store) Enabled(ctx context.Context, key string) (bool, error)`. A missing row returns false. Task 5 builds it in `cmd/main.go`.

The package is a single SQL read with no logic of its own, so it has no unit test. Step 2 checks the SQL.

- [ ] **Step 1: Write the store**

`internal/service/switches/store.go`:

```go
package switches

import (
	"context"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/lib/database"
)

const ShadowGate = "shadow_gate"

type Store struct {
	db *database.Database
}

func NewStore(db *database.Database) Store {
	return Store{db: db}
}

func (s *Store) Enabled(ctx context.Context, key string) (bool, error) {
	var on bool

	if err := s.db.QueryRow(ctx, `
		SELECT coalesce((SELECT enabled FROM switches WHERE key = $1), false)
		`, key).Scan(&on); err != nil {
		return false, fmt.Errorf("err get switch %s: %w", key, err)
	}

	return on, nil
}
```

- [ ] **Step 2: Check the SQL**

Run:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" gate_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d gate_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d gate_check' <<'SQL'
SELECT coalesce((SELECT enabled FROM switches WHERE key = 'shadow_gate'), false) AS shadow;
SELECT coalesce((SELECT enabled FROM switches WHERE key = 'missing'), false) AS missing;
UPDATE switches SET enabled = true WHERE key = 'shadow_gate';
SELECT coalesce((SELECT enabled FROM switches WHERE key = 'shadow_gate'), false) AS shadow;
SQL
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" gate_check'
```

Expected: `shadow = f`, `missing = f`, then `shadow = t`.

- [ ] **Step 3: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/service/switches/store.go
git commit -m "Add switches store

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Shadow gate decision

**Files:**
- Create: `internal/service/tagging/gate.go`
- Create: `internal/service/tagging/gate_test.go`
- Modify: `internal/service/tagging/model.go` (`TagState`)

**Interfaces:**
- Consumes: `Vocabulary{Tags map[string]TagState; Aliases map[string]string}`, `statusActive` from `model.go`.
- Produces: `TagState.Gate bool`, and `func shadowGate(v Vocabulary, targets, keys []string) *bool`. `nil` means no basis to judge.

- [ ] **Step 1: Write the failing tests**

`internal/service/tagging/gate_test.go`:

```go
package tagging

import "testing"

func gateVocabulary() Vocabulary {
	return Vocabulary{
		Tags: map[string]TagState{
			"acquisition": {Enabled: true, Status: statusActive, Gate: true},
			"investment":  {Enabled: true, Status: statusActive, Gate: true},
			"earnings":    {Enabled: true, Status: statusActive},
			"layoffs":     {Enabled: true, Status: statusDormant, Gate: true},
			"bankruptcy":  {Enabled: false, Status: statusActive, Gate: true},
		},
		Aliases: map[string]string{"investments": "investment"},
	}
}

func TestShadowGate(t *testing.T) {
	cases := []struct {
		name          string
		targets, keys []string
		want          bool
	}{
		{"target hit", []string{"earnings"}, []string{"earnings"}, true},
		{"target miss despite gate tag", []string{"earnings"}, []string{"acquisition"}, false},
		{"alias target", []string{"investments"}, []string{"investment"}, true},
		{"no targets, gate tag", nil, []string{"earnings", "acquisition"}, true},
		{"no targets, only non-gate tag", nil, []string{"earnings"}, false},
		{"no targets, disabled gate tag", nil, []string{"bankruptcy"}, false},
		{"no targets, dormant gate tag", nil, []string{"layoffs"}, false},
		{"no targets, no tags", nil, nil, false},
	}

	for _, c := range cases {
		got := shadowGate(gateVocabulary(), c.targets, c.keys)
		if got == nil || *got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestShadowGateWithoutGateTags(t *testing.T) {
	v := Vocabulary{Tags: map[string]TagState{
		"earnings":   {Enabled: true, Status: statusActive},
		"layoffs":    {Enabled: true, Status: statusDormant, Gate: true},
		"bankruptcy": {Enabled: false, Status: statusActive, Gate: true},
	}}

	if got := shadowGate(v, nil, []string{"earnings"}); got != nil {
		t.Errorf("got %v, want nil", *got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/service/tagging/ -run TestShadowGate`
Expected: FAIL to compile with `unknown field Gate in struct literal` and `undefined: shadowGate`.

- [ ] **Step 3: Add `Gate` to `TagState`**

In `internal/service/tagging/model.go`, replace:

```go
type TagState struct {
	Enabled bool
	Status  string
}
```

with:

```go
type TagState struct {
	Enabled bool
	Status  string
	Gate    bool
}
```

- [ ] **Step 4: Write `shadowGate`**

`internal/service/tagging/gate.go`:

```go
package tagging

import "slices"

func shadowGate(v Vocabulary, targets, keys []string) *bool {
	if len(targets) == 0 {
		for key, state := range v.Tags {
			if state.Gate && state.Enabled && state.Status == statusActive {
				targets = append(targets, key)
			}
		}
	}

	if len(targets) == 0 {
		return nil
	}

	passed := slices.ContainsFunc(targets, func(target string) bool {
		if key, ok := v.Aliases[target]; ok {
			target = key
		}

		return slices.Contains(keys, target)
	})

	return &passed
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/service/tagging/`
Expected: all PASS, including `TestShadowGate` and `TestShadowGateWithoutGateTags`.

- [ ] **Step 6: Commit**

```bash
git add internal/service/tagging/gate.go internal/service/tagging/gate_test.go internal/service/tagging/model.go
git commit -m "Add shadow gate decision

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Store the gate result in the tagging worker

**Files:**
- Modify: `internal/service/tagging/model.go` (`Post`)
- Modify: `internal/service/tagging/store.go` (`ListUntaggedPosts`, `CompleteTagging`, `Vocabulary`)
- Modify: `internal/service/tagging/tagged.go` (`Run`, `tagPosts`, `generateTags`)
- Modify: `cmd/main.go`

**Interfaces:**
- Consumes: `shadowGate` and `TagState.Gate` from Task 4, `switches.Store.Enabled` and `switches.ShadowGate` from Task 3, `tag_checked.gate_passed` from Task 2.
- Produces: `Post.TargetTags []string`, `func (s *Store) CompleteTagging(ctx context.Context, postID string, keys []string, gatePassed *bool) error`, `func Run(ctx context.Context, s *Store, sw *switches.Store, g *robot.TagGenerator, t *robot.Tagger, l *slog.Logger) error`, `func generateTags(ctx context.Context, s *Store, g *robot.TagGenerator, v Vocabulary, lang string, post Post) error`.

- [ ] **Step 1: Add `TargetTags` to `Post`**

In `internal/service/tagging/model.go`, replace:

```go
type Post struct {
	ID       string
	SourceID string
	Lang     string
	Title    string
	Content  string
}
```

with:

```go
type Post struct {
	ID         string
	SourceID   string
	Lang       string
	Title      string
	Content    string
	TargetTags []string
}
```

`scanPost` does not set `TargetTags`. Only `ListUntaggedPosts` needs it.

- [ ] **Step 2: Select the source's target tags in `ListUntaggedPosts`**

In `internal/service/tagging/store.go`, replace:

```go
		rows, err := s.db.Query(ctx, `
			SELECT p.id, p.source_id, p.lang, p.title, p.content
			FROM posts p
			WHERE NOT EXISTS (SELECT 1 FROM tag_checked c WHERE c.post_id = p.id)
			ORDER BY p.created_at
			LIMIT $1
			`, limit)
```

with:

```go
		rows, err := s.db.Query(ctx, `
			SELECT p.id, p.source_id, p.lang, p.title, p.content, s.target_tags
			FROM posts p
			JOIN sources s ON s.id = p.source_id
			WHERE NOT EXISTS (SELECT 1 FROM tag_checked c WHERE c.post_id = p.id)
			ORDER BY p.created_at
			LIMIT $1
			`, limit)
```

and replace:

```go
			if err := rows.Scan(&post.ID, &post.SourceID, &post.Lang, &post.Title, &post.Content); err != nil {
```

with:

```go
			if err := rows.Scan(&post.ID, &post.SourceID, &post.Lang, &post.Title, &post.Content, &post.TargetTags); err != nil {
```

- [ ] **Step 3: Store the result in `CompleteTagging`**

Replace the signature:

```go
func (s *Store) CompleteTagging(ctx context.Context, postID string, keys []string) error {
```

with:

```go
func (s *Store) CompleteTagging(ctx context.Context, postID string, keys []string, gatePassed *bool) error {
```

and replace:

```go
		_, err := tx.Exec(ctx, `INSERT INTO tag_checked (post_id) VALUES ($1) ON CONFLICT (post_id) DO NOTHING`, postID)
```

with:

```go
		_, err := tx.Exec(ctx, `
			INSERT INTO tag_checked (post_id, gate_passed) VALUES ($1, $2)
			ON CONFLICT (post_id) DO NOTHING
			`, postID, gatePassed)
```

- [ ] **Step 4: Read `gate` in `Vocabulary`**

Replace:

```go
	rows, err := s.db.Query(ctx, `SELECT key, enabled, status FROM tags`)
```

with:

```go
	rows, err := s.db.Query(ctx, `SELECT key, enabled, status, gate FROM tags`)
```

and replace:

```go
	if _, err := pgx.ForEachRow(rows, []any{&key, &state.Enabled, &state.Status}, func() error {
```

with:

```go
	if _, err := pgx.ForEachRow(rows, []any{&key, &state.Enabled, &state.Status, &state.Gate}, func() error {
```

- [ ] **Step 5: Read the switch once per tick in `Run` and `tagPosts`**

In `internal/service/tagging/tagged.go`, add the import `"github.com/dagulv/kharoborathus/internal/service/switches"`, and replace `Run` and the start of `tagPosts`:

```go
func Run(ctx context.Context, s *Store, g *robot.TagGenerator, t *robot.Tagger, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, tickInterval) {
		if err := tagPosts(ctx, s, g, t, l); err != nil {
			l.Error("tag posts", "err", err)
		}
	}

	return ctx.Err()
}

func tagPosts(ctx context.Context, s *Store, g *robot.TagGenerator, t *robot.Tagger, l *slog.Logger) error {
	posts, err := iterx.Collect(s.ListUntaggedPosts(ctx, batchSize))
	if err != nil {
		return err
	}
```

with:

```go
func Run(ctx context.Context, s *Store, sw *switches.Store, g *robot.TagGenerator, t *robot.Tagger, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, tickInterval) {
		if err := tagPosts(ctx, s, sw, g, t, l); err != nil {
			l.Error("tag posts", "err", err)
		}
	}

	return ctx.Err()
}

func tagPosts(ctx context.Context, s *Store, sw *switches.Store, g *robot.TagGenerator, t *robot.Tagger, l *slog.Logger) error {
	shadow, err := sw.Enabled(ctx, switches.ShadowGate)
	if err != nil {
		return err
	}

	posts, err := iterx.Collect(s.ListUntaggedPosts(ctx, batchSize))
	if err != nil {
		return err
	}
```

- [ ] **Step 6: Wire the gate into the post loop**

The vocabulary is now read once per post in `tagPosts` and passed to `generateTags`. Replace the loop in `tagPosts`, from `for _, post := range posts {` up to and including its closing `}`, with:

```go
	for _, post := range posts {
		lang := normalizeLang(post.Lang)

		v, err := s.Vocabulary(ctx)
		if err != nil {
			return fmt.Errorf("err vocabulary: %w", err)
		}

		if err := generateTags(ctx, s, g, v, lang, post); err != nil {
			if !errors.Is(err, adapter.ErrUnavailable) {
				l.Warn("generate tags", "post", post.ID, "err", err)
			}

			if ctx.Err() != nil {
				return nil
			}
		}

		tags, err := s.ListTags(ctx)
		if err != nil {
			return err
		}

		if len(tags) == 0 {
			continue
		}

		keys, err := t.Tags(ctx, post.Title, post.Content, tags)

		var gatePassed *bool

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			l.Warn("classify tags", "post", post.ID, "err", err)
		case shadow:
			gatePassed = shadowGate(v, post.TargetTags, keys)
		}

		if err := s.CompleteTagging(ctx, post.ID, keys, gatePassed); err != nil {
			return err
		}
	}
```

Then replace the start of `generateTags`:

```go
func generateTags(ctx context.Context, s *Store, g *robot.TagGenerator, lang string, post Post) error {
	v, err := s.Vocabulary(ctx)
	if err != nil {
		return fmt.Errorf("err vocabulary: %w", err)
	}

	generated, err := g.Generate(ctx, lang, post.Content, v.ActiveKeys())
```

with:

```go
func generateTags(ctx context.Context, s *Store, g *robot.TagGenerator, v Vocabulary, lang string, post Post) error {
	generated, err := g.Generate(ctx, lang, post.Content, v.ActiveKeys())
```

The gate reads `v` from before generation. That is safe: generation only adds terms, reactivates dormant tags and proposes new keys, and none of those change `gate`.

- [ ] **Step 7: Pass the switch store in `main.go`**

In `cmd/main.go`, add the import `"github.com/dagulv/kharoborathus/internal/service/switches"` in sorted order, and replace:

```go
	taggingStore := tagging.NewStore(db)
```

with:

```go
	taggingStore := tagging.NewStore(db)
	switchStore := switches.NewStore(db)
```

Then replace:

```go
		return tagging.Run(ctx, &taggingStore, tagGenerator, tagger, l.WithGroup("tagging"))
```

with:

```go
		return tagging.Run(ctx, &taggingStore, &switchStore, tagGenerator, tagger, l.WithGroup("tagging"))
```

- [ ] **Step 8: Build, vet and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no errors, all tests PASS.

- [ ] **Step 9: Check the SQL on a throwaway database**

Run:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" gate_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d gate_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d gate_check' <<'SQL'
INSERT INTO domains (id, base_url) VALUES ('00000000-0000-0000-0000-000000000001', 'https://d.test');
INSERT INTO sources (id, domain_id, adapter, url, target_tags)
VALUES ('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-000000000001', 'rss', 'https://d.test/a', '{investment}');
INSERT INTO posts (id, source_id, url, lang, domain_id, title, short_content, content, content_hash) VALUES
  ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', 'https://d.test/1', 'en', '00000000-0000-0000-0000-000000000001', 't', 's', 'c', '\x00'),
  ('00000000-0000-0000-0000-0000000000b2', '00000000-0000-0000-0000-0000000000a1', 'https://d.test/2', 'en', '00000000-0000-0000-0000-000000000001', 't', 's', 'c', '\x00');
INSERT INTO tags (key, gate) VALUES ('investment', true);
SELECT key, enabled, status, gate FROM tags;
SELECT p.id, p.source_id, p.lang, p.title, p.content, s.target_tags
FROM posts p
JOIN sources s ON s.id = p.source_id
WHERE NOT EXISTS (SELECT 1 FROM tag_checked c WHERE c.post_id = p.id)
ORDER BY p.created_at
LIMIT 20;
INSERT INTO tag_checked (post_id, gate_passed) VALUES ('00000000-0000-0000-0000-0000000000b1', true) ON CONFLICT (post_id) DO NOTHING;
INSERT INTO tag_checked (post_id, gate_passed) VALUES ('00000000-0000-0000-0000-0000000000b2', NULL) ON CONFLICT (post_id) DO NOTHING;
SELECT post_id, gate_passed FROM tag_checked ORDER BY post_id;
SQL
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" gate_check'
```

Expected: the tags row shows `investment | t | active | t`. The untagged query returns both posts with `target_tags = {investment}`. The last select shows `b1 | t` and `b2` with an empty `gate_passed`.

- [ ] **Step 10: Commit**

```bash
git add internal/service/tagging/model.go internal/service/tagging/store.go internal/service/tagging/tagged.go cmd/main.go
git commit -m "Store shadow gate result when tagging posts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Flag drifting sources

**Files:**
- Modify: `internal/service/tagging/store.go` (append `FlagSources`)
- Modify: `internal/service/tagging/maintain.go`
- Modify: `cmd/main.go`

**Interfaces:**
- Consumes: `tag_checked.gate_passed` from Task 5, `sources.flagged_at` and `sources.reviewed_at` from Task 2, `switches.Store` from Task 3 and `switchStore` in `cmd/main.go` from Task 5.
- Produces: `func (s *Store) FlagSources(ctx context.Context, window int, floor float64) (int64, error)`, `func RunMaintenance(ctx context.Context, s *Store, sw *switches.Store, l *slog.Logger) error`, constants `flagWindow = 50` and `flagFloor = 0.7`. Task 7's admin pass rate mirrors this query.

- [ ] **Step 1: Write the fixture and watch it fail**

The fixture builds eight sources on a throwaway database. Post `i` of a source was checked `i` hours ago, so `i = 1` is the newest. A post passes when `i <= passed`. Source `h` stores NULL results.

| Source | Posts | Passing | Other state | Expected |
|---|---|---|---|---|
| a | 60 | newest 30 | | flagged (60% of the newest 50) |
| b | 60 | newest 40 | | not flagged (80%) |
| c | 30 | none | | not flagged (window not full) |
| d | 60 | none | `reviewed_at` 90 minutes ago | not flagged (1 post after review) |
| e | 80 | newest 50 | | not flagged (window is the newest 50, all passing) |
| f | 60 | none | disabled | not flagged |
| g | 60 | none | `flagged_at` 2026-01-01 | stays flagged with the old time |
| h | 60 | NULL | | not flagged |

Save this as `.superpowers/flag_fixture.sql` (git-ignored scratch):

```sql
INSERT INTO domains (id, base_url) VALUES ('00000000-0000-0000-0000-000000000001', 'https://d.test');

INSERT INTO sources (domain_id, adapter, url, enabled, reviewed_at, flagged_at)
SELECT '00000000-0000-0000-0000-000000000001', 'rss', 'https://d.test/' || name, enabled, reviewed_at, flagged_at
FROM (VALUES
  ('a', true, NULL::timestamptz, NULL::timestamptz),
  ('b', true, NULL, NULL),
  ('c', true, NULL, NULL),
  ('d', true, now() - interval '90 minutes', NULL),
  ('e', true, NULL, NULL),
  ('f', false, NULL, NULL),
  ('g', true, NULL, '2026-01-01'),
  ('h', true, NULL, NULL)
) AS v(name, enabled, reviewed_at, flagged_at);

CREATE TEMP TABLE spec (name text, total int, passed int, judged boolean);
INSERT INTO spec VALUES
  ('a', 60, 30, true), ('b', 60, 40, true), ('c', 30, 0, true), ('d', 60, 0, true),
  ('e', 80, 50, true), ('f', 60, 0, true), ('g', 60, 0, true), ('h', 60, 0, false);

INSERT INTO posts (source_id, url, lang, domain_id, title, short_content, content, content_hash, created_at)
SELECT s.id, s.url || '/' || i, 'en', s.domain_id, 't', 's', 'c', '\x00', now() - make_interval(hours => i)
FROM spec
JOIN sources s ON s.url = 'https://d.test/' || spec.name
CROSS JOIN generate_series(1, spec.total) AS i;

INSERT INTO tag_checked (post_id, gate_passed, checked_at)
SELECT p.id, CASE WHEN spec.judged THEN split_part(p.url, '/', 5)::int <= spec.passed END, p.created_at
FROM posts p
JOIN spec ON split_part(p.url, '/', 4) = spec.name;
```

Run, with no flag query yet:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" gate_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d gate_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d gate_check' < .superpowers/flag_fixture.sql
docker compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d gate_check -c "SELECT url, flagged_at IS NOT NULL AS flagged, flagged_at < now() - interval '"'"'1 day'"'"' AS old FROM sources ORDER BY url"'
```

Expected: `a` shows `flagged = f`. That is the failure the next step fixes. Only `g` shows `t | t`. Keep `gate_check` for Step 3.

- [ ] **Step 2: Write `FlagSources`**

Append to `internal/service/tagging/store.go`:

```go
func (s *Store) FlagSources(ctx context.Context, window int, floor float64) (int64, error) {
	// Posts checked before the last review are skipped, otherwise a kept or retargeted source is flagged again the next day.
	tag, err := s.db.Exec(ctx, `
		WITH recent AS (
			SELECT
				p.source_id,
				c.gate_passed,
				row_number() OVER (PARTITION BY p.source_id ORDER BY c.checked_at DESC) AS rank
			FROM tag_checked c
			JOIN posts p ON p.id = c.post_id
			JOIN sources s ON s.id = p.source_id
			WHERE c.gate_passed IS NOT NULL
				AND c.checked_at > coalesce(s.reviewed_at, '-infinity')
		),
		rates AS (
			SELECT source_id, avg(gate_passed::int) AS rate, count(*) AS n
			FROM recent
			WHERE rank <= $1
			GROUP BY source_id
		)
		UPDATE sources s
		SET flagged_at = now()
		FROM rates
		WHERE rates.source_id = s.id
			AND s.flagged_at IS NULL
			AND s.enabled
			AND s.deleted_at IS NULL
			AND rates.n = $1
			AND rates.rate < $2
		`, window, floor)
	if err != nil {
		return 0, fmt.Errorf("err flag sources: %w", err)
	}

	return tag.RowsAffected(), nil
}
```

- [ ] **Step 3: Run the query against the fixture**

Run the SQL from Step 2 with `$1 = 50` and `$2 = 0.7` substituted, then the check select from Step 1:

```bash
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d gate_check' <<'SQL'
WITH recent AS (
	SELECT
		p.source_id,
		c.gate_passed,
		row_number() OVER (PARTITION BY p.source_id ORDER BY c.checked_at DESC) AS rank
	FROM tag_checked c
	JOIN posts p ON p.id = c.post_id
	JOIN sources s ON s.id = p.source_id
	WHERE c.gate_passed IS NOT NULL
		AND c.checked_at > coalesce(s.reviewed_at, '-infinity')
),
rates AS (
	SELECT source_id, avg(gate_passed::int) AS rate, count(*) AS n
	FROM recent
	WHERE rank <= 50
	GROUP BY source_id
)
UPDATE sources s
SET flagged_at = now()
FROM rates
WHERE rates.source_id = s.id
	AND s.flagged_at IS NULL
	AND s.enabled
	AND s.deleted_at IS NULL
	AND rates.n = 50
	AND rates.rate < 0.7;
SELECT url, flagged_at IS NOT NULL AS flagged, flagged_at < now() - interval '1 day' AS old FROM sources ORDER BY url;
SQL
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" gate_check'
```

Expected: `UPDATE 1`. Then `a | t | f`, `g | t | t`, and every other source `f` with an empty `old`. If the SQL in `store.go` and the SQL here differ, fix them so they match before you go on.

- [ ] **Step 4: Run it daily from maintenance, behind the switch**

Replace `internal/service/tagging/maintain.go` with:

```go
package tagging

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/switches"
)

const (
	maintainInterval = 24 * time.Hour
	rateWindow       = 30
	dormancyWindow   = 90
	flagWindow       = 50
	flagFloor        = 0.7
)

func RunMaintenance(ctx context.Context, s *Store, sw *switches.Store, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, maintainInterval) {
		rated, err := s.RecomputeRates(ctx, rateWindow, gateMaxRate, minSample)
		if err != nil {
			l.Error("recompute tag rates", "err", err)
		}

		dormant, err := s.MarkDormant(ctx, dormancyWindow)
		if err != nil {
			l.Error("mark dormant tags", "err", err)
		}

		flagged, err := flagSources(ctx, s, sw)
		if err != nil {
			l.Error("flag sources", "err", err)
		}

		l.Info("tag maintenance", "rated", rated, "dormant", dormant, "flagged", flagged)
	}

	return ctx.Err()
}

func flagSources(ctx context.Context, s *Store, sw *switches.Store) (int64, error) {
	on, err := sw.Enabled(ctx, switches.ShadowGate)
	if err != nil || !on {
		return 0, err
	}

	return s.FlagSources(ctx, flagWindow, flagFloor)
}
```

- [ ] **Step 5: Pass the switch store in `main.go`**

In `cmd/main.go`, replace:

```go
		return tagging.RunMaintenance(ctx, &taggingStore, l.WithGroup("maintenance"))
```

with:

```go
		return tagging.RunMaintenance(ctx, &taggingStore, &switchStore, l.WithGroup("maintenance"))
```

- [ ] **Step 6: Build, vet and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no errors, all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/tagging/store.go internal/service/tagging/maintain.go cmd/main.go
git commit -m "Flag sources whose shadow gate pass rate drops

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Admin flagged sources

**Files:**
- Modify: `frontend/src/lib/types.ts` (`Source`)
- Modify: `frontend/src/routes/admin/admin.remote.ts` (`getStats`, `listSources`, new `reviewSource` at the end of the file)
- Modify: `frontend/src/routes/admin/+page.svelte`
- Modify: `frontend/src/routes/admin/sources/+page.svelte`

**Interfaces:**
- Consumes: `sources.flagged_at`, `sources.reviewed_at` and `tag_checked.gate_passed`, plus the window from Task 6.
- Produces: `Source.flaggedAt: Date | null`, `Source.gateRate: number | null`, stat `flaggedSources`, and `reviewSource({ id, enabled, targetTags })`. The command sets `enabled` and `target_tags`, clears `flagged_at` and sets `reviewed_at = now()`.

Edit the `.svelte` files through the `svelte:svelte-file-editor` agent or the Svelte MCP tools. Before you write the client call, read the SvelteKit remote functions docs from the Svelte MCP `get-documentation` tool to confirm `command(...).updates(query)`.

- [ ] **Step 1: Extend the `Source` type**

In `frontend/src/lib/types.ts`, replace:

```ts
	postCount: number;
	lastPostedAt: Date | null;
};
```

(the end of `Source`) with:

```ts
	postCount: number;
	lastPostedAt: Date | null;
	flaggedAt: Date | null;
	gateRate: number | null;
};
```

- [ ] **Step 2: Record the type check baseline**

Run: `cd frontend && pnpm check`
Expected: `svelte-check found 0 errors`. A type change alone cannot fail, so this step only records the baseline. Steps 8 and 9 verify the task.

- [ ] **Step 3: Select the flag and the pass rate in `listSources`**

In `frontend/src/routes/admin/admin.remote.ts`, replace the body of the `listSources` SQL:

```ts
		return sql<Source[]>`
			SELECT
				s.id,
				s.domain_id,
				d.base_url AS domain_url,
				s.adapter,
				s.url,
				s.enabled,
				s.rate,
				s.target_tags,
				s.metadata,
				s.created_at,
				s.deleted_at,
				coalesce(p.post_count, 0)::int AS post_count,
				p.last_posted_at
			FROM sources s
			JOIN domains d ON d.id = s.domain_id
			LEFT JOIN (
				SELECT source_id, count(*) AS post_count, max(posted_at) AS last_posted_at
				FROM posts
				GROUP BY source_id
			) p ON p.source_id = s.id
			${includeDeleted ? sql`` : sql`WHERE s.deleted_at IS NULL`}
			ORDER BY s.created_at DESC
		`;
```

with:

```ts
		// gate_rate mirrors the window in tagging.Store.FlagSources in the Go app
		return sql<Source[]>`
			SELECT
				s.id,
				s.domain_id,
				d.base_url AS domain_url,
				s.adapter,
				s.url,
				s.enabled,
				s.rate,
				s.target_tags,
				s.metadata,
				s.created_at,
				s.deleted_at,
				coalesce(p.post_count, 0)::int AS post_count,
				p.last_posted_at,
				s.flagged_at,
				g.gate_rate
			FROM sources s
			JOIN domains d ON d.id = s.domain_id
			LEFT JOIN (
				SELECT source_id, count(*) AS post_count, max(posted_at) AS last_posted_at
				FROM posts
				GROUP BY source_id
			) p ON p.source_id = s.id
			LEFT JOIN LATERAL (
				SELECT avg(r.gate_passed::int)::real AS gate_rate
				FROM (
					SELECT c.gate_passed
					FROM tag_checked c
					JOIN posts po ON po.id = c.post_id
					WHERE po.source_id = s.id
						AND c.gate_passed IS NOT NULL
						AND c.checked_at > coalesce(s.reviewed_at, '-infinity')
					ORDER BY c.checked_at DESC
					LIMIT 50
				) r
			) g ON true
			${includeDeleted ? sql`` : sql`WHERE s.deleted_at IS NULL`}
			ORDER BY s.created_at DESC
		`;
```

- [ ] **Step 4: Count flagged sources in `getStats`**

Replace:

```ts
	const [row] = await sql<
		{ sources: number; enabledSources: number; posts: number; postsToday: number }[]
	>`
		SELECT
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL) AS sources,
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL AND enabled) AS enabled_sources,
```

with:

```ts
	const [row] = await sql<
		{
			sources: number;
			enabledSources: number;
			flaggedSources: number;
			posts: number;
			postsToday: number;
		}[]
	>`
		SELECT
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL) AS sources,
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL AND enabled) AS enabled_sources,
			(SELECT count(*)::int FROM sources WHERE deleted_at IS NULL AND flagged_at IS NOT NULL) AS flagged_sources,
```

- [ ] **Step 5: Add `reviewSource`**

Append to the end of `admin.remote.ts`, after `mergeTag`, so `tagKey` is already defined:

```ts
export const reviewSource = command(
	v.object({ id: uuid, enabled: v.boolean(), targetTags: v.array(tagKey) }),
	async ({ id, enabled, targetTags }) => {
		const sql = db();
		const [source] = await sql`
			UPDATE sources
			SET enabled = ${enabled}, target_tags = ${targetTags}::text[], flagged_at = NULL, reviewed_at = now()
			WHERE id = ${id} AND deleted_at IS NULL
			RETURNING 1
		`;

		if (!source) error(404, 'Source not found');
	}
);
```

- [ ] **Step 6: Show the flagged count on the overview**

In `frontend/src/routes/admin/+page.svelte`, replace:

```svelte
<div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
	{#each [['Sources', stats.sources], ['Enabled sources', stats.enabledSources], ['Posts', stats.posts], ['Posts (24h)', stats.postsToday]] as [label, value] (label)}
```

with:

```svelte
<div class="grid grid-cols-2 gap-4 sm:grid-cols-5">
	{#each [['Sources', stats.sources], ['Enabled sources', stats.enabledSources], ['Flagged sources', stats.flaggedSources], ['Posts', stats.posts], ['Posts (24h)', stats.postsToday]] as [label, value] (label)}
```

- [ ] **Step 7: Add the flagged panel and badge to the sources page**

In `frontend/src/routes/admin/sources/+page.svelte`, replace the import line:

```ts
	import { listSources, syncSource } from '../admin.remote';
```

with:

```ts
	import { listSources, reviewSource, syncSource } from '../admin.remote';
	import type { Source } from '#lib/types.js';
```

After the `sync` function, before `</script>`, add:

```ts
	const flagged = $derived(sources.filter((s) => s.flaggedAt && !s.deletedAt));
	const pct = new Intl.NumberFormat('sv-SE', { style: 'percent', maximumFractionDigits: 0 });

	const targetInput = new SvelteMap<string, string>();
	const reviewState = new SvelteMap<string, 'pending' | 'failed'>();

	function parseTags(text: string) {
		return [
			...new Set(
				text
					.split(',')
					.map((t) => t.trim())
					.filter(Boolean)
			)
		];
	}

	async function review(source: Source, enabled: boolean) {
		const targetTags = parseTags(targetInput.get(source.id) ?? source.targetTags.join(', '));

		reviewState.set(source.id, 'pending');
		try {
			await reviewSource({ id: source.id, enabled, targetTags }).updates(
				listSources({ includeDeleted })
			);
			reviewState.delete(source.id);
			targetInput.delete(source.id);
		} catch {
			reviewState.set(source.id, 'failed');
		}
	}
```

Between the header `</div>` and `<div class="overflow-x-auto ...">`, add:

```svelte
{#if flagged.length}
	<section class="mb-6 rounded border border-amber-300 bg-amber-50 p-4">
		<h2 class="font-medium">Flagged <span class="text-zinc-500">({flagged.length})</span></h2>
		<p class="mb-3 text-sm text-zinc-600">
			Fewer than 70% of the last 50 posts passed the shadow gate. Keep saves the target tags below
			and restarts the count.
		</p>
		<ul class="space-y-2">
			{#each flagged as source (source.id)}
				{@const status = reviewState.get(source.id)}
				<li class="flex flex-wrap items-center gap-2 text-sm">
					<a
						class="min-w-0 flex-1 truncate hover:underline"
						href="/admin/posts?source={source.id}"
						title={source.url}
					>
						{source.url}
					</a>
					<span class="text-zinc-600 tabular-nums">
						{source.gateRate === null ? '—' : pct.format(source.gateRate)} pass
					</span>
					<input
						class="w-56 rounded border border-zinc-300 bg-white px-2 py-0.5"
						placeholder="target tags, comma separated"
						value={targetInput.get(source.id) ?? source.targetTags.join(', ')}
						oninput={(e) => targetInput.set(source.id, e.currentTarget.value)}
					/>
					<button
						type="button"
						class="rounded border border-zinc-300 bg-white px-2 py-0.5 text-xs hover:bg-zinc-100 disabled:opacity-50"
						disabled={status === 'pending'}
						onclick={() => review(source, true)}
					>
						Keep
					</button>
					<button
						type="button"
						class="rounded border border-red-300 bg-white px-2 py-0.5 text-xs text-red-700 hover:bg-red-50 disabled:opacity-50"
						disabled={status === 'pending'}
						onclick={() => review(source, false)}
					>
						Disable
					</button>
					{#if status === 'failed'}<span class="text-xs text-red-600">Failed</span>{/if}
				</li>
			{/each}
		</ul>
	</section>
{/if}
```

In the status cell of the table, replace:

```svelte
						{:else}
							<span class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-600">disabled</span>
						{/if}
```

with:

```svelte
						{:else}
							<span class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-600">disabled</span>
						{/if}
						{#if source.flaggedAt && !source.deletedAt}
							<span class="rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-800">flagged</span>
						{/if}
```

- [ ] **Step 8: Type check and autofix**

Run: `cd frontend && pnpm check`
Expected: `svelte-check found 0 errors`.

Run the Svelte MCP `svelte-autofixer` on `sources/+page.svelte` and `+page.svelte`. Expected: no issues.

- [ ] **Step 9: Check the admin SQL on a throwaway database**

Rebuild `gate_check` and load the Task 6 fixture as in Task 6 Step 1. Then run the pass rate part of `listSources` and the `reviewSource` update for source `a`:

```bash
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d gate_check' <<'SQL'
SELECT s.url, round(g.gate_rate::numeric, 2) AS gate_rate
FROM sources s
LEFT JOIN LATERAL (
	SELECT avg(r.gate_passed::int)::real AS gate_rate
	FROM (
		SELECT c.gate_passed
		FROM tag_checked c
		JOIN posts po ON po.id = c.post_id
		WHERE po.source_id = s.id
			AND c.gate_passed IS NOT NULL
			AND c.checked_at > coalesce(s.reviewed_at, '-infinity')
		ORDER BY c.checked_at DESC
		LIMIT 50
	) r
) g ON true
WHERE s.deleted_at IS NULL
ORDER BY s.url;
UPDATE sources
SET enabled = true, target_tags = '{investment}'::text[], flagged_at = NULL, reviewed_at = now()
WHERE url = 'https://d.test/a' AND deleted_at IS NULL
RETURNING url, flagged_at, reviewed_at IS NOT NULL AS reviewed;
SQL
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" gate_check'
```

Expected rates: `a 0.60`, `b 0.80`, `c 0.00`, `d 0.00`, `e 1.00`, `f 0.00`, `g 0.00`, `h` empty. The update returns one row for `a` with an empty `flagged_at` and `reviewed = t`.

- [ ] **Step 10: Commit**

```bash
git add frontend/src/lib/types.ts frontend/src/routes/admin/admin.remote.ts frontend/src/routes/admin/+page.svelte frontend/src/routes/admin/sources/+page.svelte
git commit -m "Add flagged sources review to admin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Knowledge base

**Files:**
- Modify: `docs/kb/classifier-gate.md`
- Modify: `docs/kb/architecture.md`
- Modify: `docs/kb/rollout.md`
- Modify: `docs/kb/index.md`

**Interfaces:**
- Consumes: the rulings at the top of this plan.

- [ ] **Step 1: Update `classifier-gate.md`**

- Set `**Status:** shadow gate done, hard gate designed`.
- Replace the Current state paragraph with: "While the `shadow_gate` switch is on, the tagging worker stores the shadow gate result in `tag_checked.gate_passed`, see [gate.go](../../internal/service/tagging/gate.go). Daily maintenance sets `sources.flagged_at`, and admin lists flagged sources. The hard gate is not built. It ships with [discovered ingest](discovered-ingest.md) in phase 4."
- Under shadow gate step 2, add: "Target tags are resolved through `tag_aliases`."
- Replace shadow gate step 3 with: "Store the result. Never reject the post. The result is NULL when the classification failed, or when the source has no target tags and no gate tag exists."
- Replace shadow gate step 6 with: "You review flagged sources only, and keep, retarget or disable them. Every review clears `flagged_at` and sets `reviewed_at`. The window only counts posts checked after `reviewed_at`, so a reviewed source needs a fresh full window before it can be flagged again."
- Add `ALTER TABLE sources ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;` to Schema changes.

- [ ] **Step 2: Update `architecture.md`**

- In the Sources worker row, replace "Polls active sources" with "Polls enabled sources".
- In the Tagging worker row, replace the job with "Generates new tags, classifies posts against all tags and records the shadow gate result while its switch is on."
- Remove the bullet "`sources.target_tags` is saved from admin but never read."

- [ ] **Step 3: Update `rollout.md`**

- Set Current state to: "Phases 1 and 2 are done, with the switches table and admin page. `shadow_gate` starts off."
- Replace the "**Every phase:**" migration bullet with: "**Every phase:** until the first deploy, schema changes go straight into `schema.sql` and databases are recreated from it."
- In the "**Phases 2, 4, 5 and 6:**" bullet, replace "the migration inserts" with "`schema.sql` inserts".

- [ ] **Step 4: Update `index.md`**

Set the Classifier gate row status to `shadow done`.

- [ ] **Step 5: Check the edits**

Run: `grep -n "gate.go\|reviewed_at\|shadow done\|recreated from it" docs/kb/*.md`
Expected: `classifier-gate.md` shows the new link and `reviewed_at`, `index.md` shows `shadow done`, and `rollout.md` shows the new migration bullet. `ls internal/service/tagging/gate.go` exists.

- [ ] **Step 6: Commit**

```bash
git add docs/kb/classifier-gate.md docs/kb/architecture.md docs/kb/rollout.md docs/kb/index.md
git commit -m "Describe shipped shadow gate and switches in kb

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
