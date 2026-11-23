# Logs Retention and 30 GB Disk Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep Postgres inside the 30 GB Standard disk that `STARTER-2xCPU-4GB` includes, so the server costs €12 a month with no extra storage.

**Architecture:** A daily job in `internal/lib/logs`, the package that already writes the `logs` table, deletes rows older than 30 days in batches. `step` rows stay, since the post trace is built from them and they are small. The OpenTofu disk default drops from 50 to 30 GB. Postgres stays on the system disk, since a separate volume bills every GB.

**Tech Stack:** Go, pgx via `internal/lib/database`, `internal/lib/schedule`, OpenTofu with the UpCloud provider.

**Spec:** This plan. The sizing comes from the dev database on 2026-11-23: 125 MB for 521 posts, 82% of it in `logs`.

## Global Constraints

- Retention: 30 days, by `created_at`.
- Kept forever: rows with `type = 'step'`. Average 341 bytes each.
- Batch size: 10,000 rows per `DELETE`, so the first run after deploy does not hold one huge transaction.
- Interval: once at startup, then every 24 hours, through `schedule.Always.Tick`.
- Disk: `storage_size` default 30, tier `standard`, plan `STARTER-2xCPU-4GB`.
- No new index on `logs`. The batch walks the primary key from the oldest id.

## Sizing

The OS, Docker and images take about 8 GB of 30, which leaves about 22 GB for Postgres.

| Posts/day, sources | `logs` with retention, steady | Other tables, per year | Disk at 22 GB |
|---|---|---|---|
| 100, 20 | ~1.3 GB | ~1.3 GB | >10 years |
| 400, 50 | ~3.4 GB | ~5 GB | ~3.5 years |
| 1,500, 100 | ~11 GB | ~19 GB | ~7 months |

Deleted rows free space for reuse through autovacuum. The files never shrink, so the steady size is the peak.

## Review Focus

- A trace of a post older than 30 days loses its DeepSeek, Jev and fetch rows. It keeps its steps. `TestPruneKeepsSteps` pins that steps survive.
- The rule call in a trace links to the latest `deepseek` row for the domain. If the rule is older than 30 days, `ruleCall` returns nil and the trace shows no rule call. It already handles a nil result, so nothing breaks.
- A backlog larger than one batch has to be fully deleted in one run. `TestPruneDeletesAllBatches` pins it with a batch size of 2.
- A failed delete must not stop the app. `Prune` logs the error and waits for the next tick.
- A row exactly 30 days old stays. The cutoff is `created_at < now - 30 days`.

---

### Task 1: Prune old logs rows

**Files:**
- Create: `internal/lib/logs/prune.go`
- Create: `internal/lib/logs/prune_test.go`
- Modify: `cmd/main.go`, one `g.Go` next to the other workers
- Modify: `docs/kb/request-logs.md`, the "No retention" row and the matching retention option under "Ways to change it later"

**Interfaces:**
- Produces: `func Prune(ctx context.Context, db *database.Database, l *slog.Logger)`, which blocks until `ctx` ends.
- Produces: `func deleteBefore(ctx context.Context, db *database.Database, before time.Time, batch int) (int64, error)`, which returns the total number of rows deleted.
- Constants in `prune.go`: `retention = 30 * 24 * time.Hour`, `pruneBatch = 10_000`.

- [ ] **Step 1: Write the failing tests** in `prune_test.go`, using `dbtest.New(t)`. Insert rows with explicit `created_at`.

```go
func TestPruneDeletesOldRows(t *testing.T)
// rows: rss at now-31d, deepseek at now-40d, rss at now-29d
// deleteBefore(ctx, db, now-30d, 10_000) returns 2
// remaining types: [rss], the now-29d row

func TestPruneKeepsSteps(t *testing.T)
// rows: step at now-90d, jev at now-90d
// deleteBefore(..., now-30d, 10_000) returns 1, the step row remains

func TestPruneDeletesAllBatches(t *testing.T)
// 5 rss rows at now-31d
// deleteBefore(..., now-30d, 2) returns 5, count(*) = 0
```

- [ ] **Step 2: Run them to verify they fail**

Run: `TEST_POSTGRES_CONN=<test db conn> go test ./internal/lib/logs -run TestPrune -v`
Expected: FAIL, `undefined: deleteBefore`.

- [ ] **Step 3: Implement `deleteBefore` and `Prune` in `prune.go`**

`deleteBefore` loops until a batch deletes fewer than `batch` rows:

```sql
DELETE FROM logs WHERE id IN (
  SELECT id FROM logs WHERE created_at < $1 AND type <> 'step' ORDER BY id LIMIT $2
)
```

Wrap errors as `err delete logs: %w`. `Prune` ranges over `schedule.Always.Tick(ctx, 24*time.Hour)` and calls `deleteBefore(ctx, db, time.Now().Add(-retention), pruneBatch)`. It logs `l.Error("prune logs", "err", err)` unless `ctx.Err() != nil`, and on success logs `l.Info("prune logs", "deleted", n)`. Follow the shape of `retryPending` in `internal/core/service/source/retry.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TEST_POSTGRES_CONN=<test db conn> go test ./internal/lib/logs -v`
Expected: PASS, including the existing handler tests.

- [ ] **Step 5: Wire it in `cmd/main.go`**

```go
g.Go(func() error {
	logs.Prune(ctx, db, l.WithGroup("prune"))
	return nil
})
```

Run: `go build ./... && go vet ./cmd/...`
Expected: no output.

- [ ] **Step 6: Update `docs/kb/request-logs.md`**

The retention row becomes: 30 days, `step` rows kept, deleted daily in batches of 10,000. Point it at `prune.go`. Remove the retention option from "Ways to change it later", since it is done.

- [ ] **Step 7: Commit**

```bash
git add internal/lib/logs/prune.go internal/lib/logs/prune_test.go cmd/main.go docs/kb/request-logs.md
git commit -m "Delete logs rows older than 30 days, keeping pipeline steps"
```

### Task 2: Shrink the disk to the plan's 30 GB

**Files:**
- Modify: `infra/tofu/variables.tf`, `storage_size` default `50` → `30`
- Modify: `docs/kb/infra.md`, the Defaults table

**Interfaces:** none.

- [ ] **Step 1: Change the default** in `variables.tf`. Keep the description.

- [ ] **Step 2: Update `docs/kb/infra.md`**

The Disk row becomes `30 GB`, with the reason: "The Starter plan includes 30 GB. Postgres fits for years with 30-day `logs` retention. Grows with `size`, billed per extra GB." Add an open question: "Haloy's image history and Docker build cache on the 30 GB disk. Check `docker system df` after the first deploys."

- [ ] **Step 3: Verify**

Run: `grep -n 'default     = 30' infra/tofu/variables.tf`
Expected: one hit under `storage_size`. `tofu validate` is blocked for agents, so the user runs `tofu plan` and checks that the template size shows 30.

- [ ] **Step 4: Commit**

```bash
git add infra/tofu/variables.tf docs/kb/infra.md
git commit -m "Size the disk to the Starter plan's included 30 GB"
```
