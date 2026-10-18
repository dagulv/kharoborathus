# Quality Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every post passes a Jev quality check before it is listed or reaches any DeepSeek call.

**Architecture:** `posts.status` moves from `pending` to `active` or `rejected` in a new quality worker. Summary, tagging, discovery and link extraction read `active` posts only, side by side, each through its own checked table. Summaries leave the source pipeline and run off peak from a `summary_checked` queue.

**Tech Stack:** Go 1.27, PostgreSQL through pgx v5, tern migrations, Jev classifier, DeepSeek LLM.

**Spec:** `docs/kb/quality-gate.md`

## Global Constraints

- Status values: `pending`, `active`, `rejected`. Default `pending`.
- Quality threshold: 0.8, the existing `tagThreshold`.
- Quality batch: 20 posts a minute, oldest first.
- Summary batch: 20 posts a minute, off peak, existing `summaryBatch`.
- Existing posts become `active` without a check and keep their summaries.
- Queries joining `sources` or `tags` must write `p.status`.
- `summary_pending` stays in the schema. It is dropped in a later migration after deploy, not in this plan.
- Shipped migration files are never edited. Schema changes go in `internal/migrations/002_post_status.sql`.
- Go style: `any`, wrapped errors in leaf calls, guard clauses, optimistic assignment. Comment only non-obvious intent.
- One commit per single purpose. Never push.

## Review Focus

1. Jev returns 500 or a network error. The post must stay `pending` and be retried, never rejected. Owned by Task 3.
2. Jev answers 429 or 503. The tick stops and every post stays `pending`. Owned by Task 3.
3. Dev runs with the mocked classifier. Every post must become `active`, or Swedish posts vanish from the dev feed. Owned by Task 3.
4. A `pending` or `rejected` post is requested by id at `/feeds/{id}`. It must not be served. Owned by Task 2.
5. Migration on a database with posts. Every existing post is `active`, and posts already summarized get a `summary_checked` row. Owned by Task 2.

---

### Task 1: Tagger.Quality

**Files:**
- Modify: `internal/service/robot/gate.go`
- Test: `internal/service/robot/gate_test.go`

**Interfaces:**
- Consumes: `Tagger.classify`, `articlePrompt`, `articleCriteria`, `articleID`, `tagThreshold`.
- Produces: `func (t *Tagger) Quality(ctx context.Context, l *slog.Logger, title, markdown string) (bool, error)`. Errors wrap the classifier error, so `errors.Is(err, adapter.ErrUnavailable)` holds.

- [ ] **Step 1: Write the failing tests**

First run `grep -n "errClassifier" internal/service/robot/*_test.go`. Expected: no output. Pick another name if it exists.

Add `"errors"` and `"github.com/dagulv/kharoborathus/internal/service/robot/adapter"` to the imports of `gate_test.go`, then append:

```go
func TestQualityPasses(t *testing.T) {
	c := &fakeClassifier{yes: map[string]bool{strings.TrimSpace(articlePrompt): true}}

	passed, err := NewTagger(c).Quality(context.Background(), slog.New(slog.DiscardHandler), "Volvo köper", "Volvo köper Northvolt.")
	if err != nil {
		t.Fatal(err)
	}

	if !passed {
		t.Error("want passed")
	}

	if len(c.requests) != 1 || len(c.requests[0]) != 1 {
		t.Fatalf("requests %+v", c.requests)
	}

	if q := c.requests[0][articleID]; q.Criteria == nil || q.Criteria.True == "" || q.Criteria.False == "" {
		t.Errorf("article question %+v", q)
	}
}

func TestQualityRejects(t *testing.T) {
	c := &fakeClassifier{}

	passed, err := NewTagger(c).Quality(context.Background(), slog.New(slog.DiscardHandler), "Kategori", "Lista över artiklar.")
	if err != nil {
		t.Fatal(err)
	}

	if passed {
		t.Error("want rejected")
	}
}

func TestQualityUnavailable(t *testing.T) {
	_, err := NewTagger(errClassifier{}).Quality(context.Background(), slog.New(slog.DiscardHandler), "Volvo köper", "Volvo köper Northvolt.")
	if !errors.Is(err, adapter.ErrUnavailable) {
		t.Errorf("err %v", err)
	}
}

type errClassifier struct{}

func (errClassifier) Noul(context.Context, *slog.Logger, any, map[string]adapter.NoulQuestion) (map[string]float64, error) {
	return nil, adapter.ErrUnavailable
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/service/robot/ -run Quality`
Expected: FAIL with `NewTagger(c).Quality undefined`.

- [ ] **Step 3: Implement**

In `gate.go`, add `"fmt"` to imports. Replace the inline question map in `Gate` with a shared helper and add `Quality`:

```go
func articleQuestion() map[string]adapter.NoulQuestion {
	return map[string]adapter.NoulQuestion{
		articleID: {Instructions: strings.TrimSpace(articlePrompt), Criteria: &articleCriteria},
	}
}

func (t *Tagger) Gate(ctx context.Context, l *slog.Logger, title, markdown string, tags []Tag) (Verdict, error) {
	scores, extra, err := t.classify(ctx, l, title, markdown, tags, articleQuestion())
	if err != nil {
		return Verdict{}, err
	}
	// rest of Gate unchanged
}

func (t *Tagger) Quality(ctx context.Context, l *slog.Logger, title, markdown string) (bool, error) {
	_, extra, err := t.classify(ctx, l, title, markdown, nil, articleQuestion())
	if err != nil {
		return false, fmt.Errorf("err classify quality: %w", err)
	}

	return extra[articleID] >= tagThreshold, nil
}
```

With no tags, `classify` still sends one request, because its loop runs once for `start == 0` and the extra questions are added there.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/service/robot/`
Expected: PASS, including the existing `TestGate*` tests.

- [ ] **Step 5: Commit**

```bash
git add internal/service/robot/gate.go internal/service/robot/gate_test.go
git commit -m "Ask Jev the article question alone as a quality check"
```

---

### Task 2: Post status in schema and feed store

**Files:**
- Create: `internal/migrations/002_post_status.sql`
- Modify: `internal/service/feed/model.go`
- Modify: `internal/service/feed/store.go`
- Modify: `internal/service/ingest/ingest.go:283`

**Interfaces:**
- Produces:
  - `feed.StatusPending`, `feed.StatusActive`, `feed.StatusRejected` string constants.
  - `Feed.Status string`, `json:"-"`. Empty inserts as `pending`.
  - `func (s *Store) ListPendingQuality(ctx context.Context, limit int) ([]Feed, error)`
  - `func (s *Store) SetStatus(ctx context.Context, id, status string) error`
  - Table `summary_checked (post_id, checked_at)`.

- [ ] **Step 1: Write the migration**

`internal/migrations/002_post_status.sql`:

```sql
-- Existing posts are active without a check. New posts wait for the quality worker.
ALTER TABLE posts ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'
  CHECK (status IN ('pending', 'active', 'rejected'));

ALTER TABLE posts ALTER COLUMN status SET DEFAULT 'pending';

CREATE INDEX IF NOT EXISTS posts_status_pending_idx ON posts (created_at) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS summary_checked (
  post_id UUID PRIMARY KEY REFERENCES posts (id) ON DELETE CASCADE,
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO summary_checked (post_id)
SELECT id FROM posts WHERE NOT summary_pending
ON CONFLICT DO NOTHING;
```

- [ ] **Step 2: Add status to the model**

In `feed/model.go`, above `type Feed`:

```go
const (
	StatusPending  = "pending"
	StatusActive   = "active"
	StatusRejected = "rejected"
)
```

In `Feed`, after `SummaryPending`:

```go
	// Empty inserts as pending, see source.RunQuality.
	Status string `json:"-"`
```

- [ ] **Step 3: Write status on insert**

In `InsertFeed`, after the `PostedAt` guard:

```go
	status := StatusPending
	if feed.Status != "" {
		status = feed.Status
	}
```

Change the insert to:

```go
		INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, tags, authors, posted_at, content_hash, simhash, summary_pending, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], '{}'), COALESCE($9::text[], '{}'), $10, $11, $12, $13, $14)
```

and append `status` to its arguments after `feed.SummaryPending`.

- [ ] **Step 4: Hide non-active posts from feed reads**

In `ListPosts`, start the conditions with the status filter and always emit `WHERE`:

```go
		conds := []string{`p.status = 'active'`}
```

Delete `var conds []string`, and replace the `var where string` block with:

```go
		where := "WHERE " + strings.Join(conds, " AND ")
```

In `GetFeed`:

```go
	return scanFeed(s.db.QueryRow(ctx, `SELECT `+feedColumns+` FROM posts WHERE id = $1 AND status = 'active'`, id))
```

- [ ] **Step 5: Add quality queue queries**

Append to `feed/store.go`:

```go
func (s *Store) ListPendingQuality(ctx context.Context, limit int) ([]Feed, error) {
	rows, err := s.db.Query(ctx, `SELECT `+feedColumns+` FROM posts WHERE status = 'pending' ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("err query pending quality: %w", err)
	}

	feeds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Feed, error) {
		return scanFeed(row)
	})
	if err != nil {
		return nil, fmt.Errorf("err scan pending quality: %w", err)
	}

	return feeds, nil
}

func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	if _, err := s.db.Exec(ctx, `UPDATE posts SET status = $2 WHERE id = $1`, id, status); err != nil {
		return fmt.Errorf("err set post status: %w", err)
	}

	return nil
}
```

- [ ] **Step 6: Insert discovered posts as active**

In `ingest.go` `process`, after `f.Tags = v.Keys`:

```go
	f.Status = feed.StatusActive
```

- [ ] **Step 7: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no errors, all PASS.

- [ ] **Step 8: Migrate the dev database and check it**

Run: `docker compose up -d db && go run ./cmd migrate`
Expected: a `migrating` line for `002_post_status.sql`, then `schema up to date` with version 2. If `.env` is missing, stop and ask the user to run `infra/setup.sh`.

Run:

```bash
docker compose exec db sh -c 'psql -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-postgres}" -c "SELECT status, count(*) FROM posts GROUP BY 1" -c "SELECT (SELECT count(*) FROM summary_checked) AS checked, (SELECT count(*) FROM posts WHERE NOT summary_pending) AS summarized"'
```

Expected: only `active` rows, and `checked` equals `summarized`.

- [ ] **Step 9: Commit**

```bash
git add internal/migrations/002_post_status.sql internal/service/feed/model.go internal/service/feed/store.go internal/service/ingest/ingest.go
git commit -m "Give posts a status and list only active ones"
```

---

### Task 3: Quality worker

**Files:**
- Create: `internal/service/source/quality.go`
- Modify: `cmd/main.go`

**Interfaces:**
- Consumes: `Tagger.Quality` from Task 1. `feed.Store.ListPendingQuality`, `feed.Store.SetStatus` and the status constants from Task 2.
- Produces: `func RunQuality(ctx context.Context, s *Store, tagger *robot.Tagger, mocked bool, l, calls *slog.Logger) error`

The repo does not unit test worker loops, see `summaries.go` and `tagged.go`. The classifier logic is covered by Task 1. This task is verified by build and a dev run.

- [ ] **Step 1: Write the worker**

`internal/service/source/quality.go`:

```go
package source

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	robotadapter "github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const (
	qualityInterval = time.Minute
	qualityBatch    = 20
)

// The mocked classifier cannot judge quality, so mocked activates every post.
func RunQuality(ctx context.Context, s *Store, tagger *robot.Tagger, mocked bool, l, calls *slog.Logger) error {
	for range schedule.Always.Tick(ctx, qualityInterval) {
		if err := checkQuality(ctx, s, tagger, mocked, l, calls); err != nil {
			l.Error("check quality", "err", err)
		}
	}

	return ctx.Err()
}

func checkQuality(ctx context.Context, s *Store, tagger *robot.Tagger, mocked bool, l, calls *slog.Logger) error {
	posts, err := s.feedStore.ListPendingQuality(ctx, qualityBatch)
	if err != nil {
		return err
	}

	counts := make(map[string]int)

	defer func() {
		if len(counts) > 0 {
			l.Info("check quality", "counts", counts)
		}
	}()

	for _, post := range posts {
		passed := true
		var err error

		if !mocked {
			passed, err = tagger.Quality(ctx, calls.With("source_id", post.SourceID, "post_id", post.ID, "url", post.URL), post.Title, post.Content)
		}

		switch {
		case errors.Is(err, robotadapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		// Jev answers 500 and network errors without ErrUnavailable. Rejecting on them would hide good posts during an outage.
		case err != nil:
			l.Warn("check quality", "post", post.ID, "err", err)
			counts["error"]++
			continue
		}

		status := feed.StatusRejected
		if passed {
			status = feed.StatusActive
		}

		if err := s.feedStore.SetStatus(ctx, post.ID, status); err != nil {
			return err
		}

		counts[status]++
	}

	return nil
}
```

- [ ] **Step 2: Wire it in main**

In `cmd/main.go`, after the `RunSummaries` goroutine:

```go
	g.Go(func() error {
		if mocked {
			l.Warn("quality gate passes every post, the mocked classifier cannot judge quality")
		}

		return source.RunQuality(ctx, &sourceStore, tagger, mocked, l.WithGroup("quality"), calls)
	})
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no errors, all PASS.

- [ ] **Step 4: Dev run**

Ask the user first. `go run ./cmd` starts every worker, including real source fetches and DeepSeek calls with dev secrets.

If approved, run `go run ./cmd` for 2 minutes. Then rerun the status count query from Task 2 Step 8.
Expected: a `check quality` log line, and new posts move out of `pending`.

- [ ] **Step 5: Commit**

```bash
git add internal/service/source/quality.go cmd/main.go
git commit -m "Activate or reject pending posts with the Jev quality check"
```

---

### Task 4: Summaries from the off-peak queue only

**Files:**
- Modify: `internal/service/feed/store.go`
- Modify: `internal/service/feed/model.go`
- Modify: `internal/service/source/sourced.go`
- Modify: `internal/service/source/summaries.go:19`
- Modify: `internal/service/source/adapter/wordpress.go:300-306`
- Modify: `internal/service/source/adapter/wordpress_test.go:142-145`
- Modify: `internal/service/ingest/ingest.go`
- Modify: `cmd/main.go`

**Interfaces:**
- Consumes: `summary_checked` and `posts.status` from Task 2.
- Produces:
  - `source.Run(ctx, s, syncs, trim, sched, l, calls)` without the summarizer.
  - `ingest.NewIngester(s, sw, tags, feeds, domains, trim, tagger, l, calls)` without the summarizer.
  - `feed.Feed` without `SummaryPending`.

- [ ] **Step 1: Queue summaries by summary_checked**

In `feed/store.go`, replace `ListPendingSummaries` and `CompleteSummary`:

```go
func (s *Store) ListPendingSummaries(ctx context.Context, limit int) ([]Feed, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+feedColumns+`
		FROM posts p
		WHERE p.status = 'active'
			AND NOT EXISTS (SELECT 1 FROM summary_checked c WHERE c.post_id = p.id)
		ORDER BY p.created_at
		LIMIT $1
		`, limit)
	if err != nil {
		return nil, fmt.Errorf("err query pending summaries: %w", err)
	}

	feeds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Feed, error) {
		return scanFeed(row)
	})
	if err != nil {
		return nil, fmt.Errorf("err scan pending summaries: %w", err)
	}

	return feeds, nil
}

// An empty summary keeps the excerpt, so a post the summarizer cannot handle still leaves the queue.
func (s *Store) CompleteSummary(ctx context.Context, id, summary string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE posts SET short_content = coalesce(nullif($2, ''), short_content) WHERE id = $1`, id, summary); err != nil {
			return fmt.Errorf("err complete summary: %w", err)
		}

		if _, err := tx.Exec(ctx, `INSERT INTO summary_checked (post_id) VALUES ($1) ON CONFLICT (post_id) DO NOTHING`, id); err != nil {
			return fmt.Errorf("err mark summarized: %w", err)
		}

		return nil
	})
}
```

- [ ] **Step 2: Drop SummaryPending from the model and insert**

In `feed/model.go`, delete the `SummaryPending` field and its comment.

In `InsertFeed`, the insert becomes:

```go
		INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, tags, authors, posted_at, content_hash, simhash, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], '{}'), COALESCE($9::text[], '{}'), $10, $11, $12, $13)
```

Its arguments end with `textSimhash(feed.Content), status,`. The column default fills `summary_pending` until the later migration drops it.

- [ ] **Step 3: Remove the WordPress pending flag**

In `wordpress.go`, delete the `ProcessFeed` override and its comment. `wordpress` embeds `rss`, so `rss.ProcessFeed` serves it.

In `wordpress_test.go`, delete:

```go
	f, err := wp.ProcessFeed(context.Background(), a)
	if err != nil || !f.SummaryPending {
		t.Errorf("summary pending %v, %v", f.SummaryPending, err)
	}
```

- [ ] **Step 4: Remove the pipeline summary stage**

In `sourced.go`:
- Delete `summarizeFeed`.
- In `pipeline.Run`, end with `return fanOut(ctx, workers, trimmed, l, p.markdownFeed)` and delete the `converted` line.
- Remove the `summarizer` field from `pipeline`, and the `summarizer *robot.Summarizer` parameter from `newPipeline`, `initPipelines` and `Run`, with every argument that passed it.
- Remove imports this leaves unused. `go build` lists them.

In `summaries.go`, change the `RunSummaries` comment to:

```go
// RunSummaries summarizes active posts, a batch a minute while the LLM is open.
```

In `cmd/main.go`:

```go
		return source.Run(ctx, &sourceStore, syncs, trim, schedule.Always, l.WithGroup("sourced"), calls)
```

- [ ] **Step 5: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no errors, all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/feed internal/service/source cmd/main.go
git commit -m "Summarize active posts off peak instead of in the pipeline"
```

- [ ] **Step 7: Remove the inline summary from ingest**

In `ingest.go`:
- Delete the `summarizer` field, the `summarizer *robot.Summarizer` parameter of `NewIngester` and its assignment.
- In `process`, delete the second `res.aiCalls++` and the `in.summarizer.Summarize` block after it.

In `cmd/main.go`:

```go
	ingester := ingest.NewIngester(&ingestStore, &switchStore, &taggingStore, &feedStore, &domainStore, trim, tagger, l.WithGroup("ingest"), calls)
```

`summarizer` is still used by `RunSummaries`, so keep `robot.NewSummarizer`.

- [ ] **Step 8: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no errors, all PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/service/ingest/ingest.go cmd/main.go
git commit -m "Leave discovered post summaries to the off-peak queue"
```

---

### Task 5: Downstream workers read active posts only

**Files:**
- Modify: `internal/service/tagging/store.go:47-54` `ListUntaggedPosts`
- Modify: `internal/service/tagging/store.go:311-322` `ListRecentPosts`
- Modify: `internal/service/discovery/store.go:27-33` `ListUndiscoveredPosts`
- Modify: `internal/service/ingest/store.go:21-33` `ListUnscannedPosts`

**Interfaces:**
- Consumes: `posts.status` from Task 2.

- [ ] **Step 1: Filter tagging**

`ListUntaggedPosts` where clause:

```sql
			WHERE p.status = 'active'
				AND NOT EXISTS (SELECT 1 FROM tag_checked c WHERE c.post_id = p.id)
```

`ListRecentPosts`, after `WHERE p.created_at > now() - make_interval(days => $1)`:

```sql
			AND p.status = 'active'
```

- [ ] **Step 2: Filter discovery**

`ListUndiscoveredPosts` where clause:

```sql
			WHERE p.status = 'active'
				AND NOT EXISTS (SELECT 1 FROM discover_checked c WHERE c.post_id = p.id)
```

- [ ] **Step 3: Filter link extraction**

`ListUnscannedPosts`, after `WHERE s.status IN ('active', 'dormant')`:

```sql
			AND p.status = 'active'
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no errors, all PASS.

- [ ] **Step 5: Check the queries parse on the dev database**

```bash
docker compose exec db sh -c 'psql -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-postgres}" -c "EXPLAIN SELECT p.id FROM posts p JOIN sources s ON s.id = p.source_id WHERE p.status = '"'"'active'"'"' AND s.status IN ('"'"'active'"'"', '"'"'dormant'"'"') AND NOT EXISTS (SELECT 1 FROM tag_checked c WHERE c.post_id = p.id)"'
```

Expected: a query plan, no `ambiguous` error.

- [ ] **Step 6: Commit**

```bash
git add internal/service/tagging/store.go internal/service/discovery/store.go internal/service/ingest/store.go
git commit -m "Keep tagging, discovery and links on active posts"
```

---

### Task 6: Docs

**Files:**
- Modify: `docs/kb/quality-gate.md`
- Modify: `docs/kb/index.md`
- Modify: `docs/kb/architecture.md`
- Modify: `docs/kb/backfill.md`
- Modify: `docs/kb/trust-model.md`

- [ ] **Step 1: Mark the spec done**

In `quality-gate.md`, set `**Status:** done` and replace `## Current state` body with:

```markdown
[quality.go](../../internal/service/source/quality.go) checks `pending` posts with `Tagger.Quality` in [gate.go](../../internal/service/robot/gate.go). [summaries.go](../../internal/service/source/summaries.go) summarizes `active` posts through `summary_checked`. Tagging, discovery and link extraction read `active` posts only. `summary_pending` is still in the schema until the follow-up migration.
```

In `index.md`, set the Quality gate row status to `done`.

- [ ] **Step 2: Update architecture**

In `architecture.md` workers table, change the Summaries row job to `Summarizes active posts off peak.` and add after it:

```markdown
| Quality         | [quality.go](../../internal/service/source/quality.go)                                                           | Every minute: activates or rejects pending posts with the Jev [quality gate](quality-gate.md).                                                           |
```

Replace source pipeline steps 7 and 8 with:

```markdown
7. Insert into `posts` as `pending` with a simhash and link `duplicate_of`.
8. The [quality gate](quality-gate.md) sets `active` or `rejected`. Summary, tagging, discovery and links follow on `active` posts.
```

In the AI providers list, change the Jev line to `Jev is the classifier. It answers yes or no questions as scores from 0 to 1, including the quality gate on every post.`

- [ ] **Step 3: Update backfill**

In `backfill.md`:
- Current state bullet: `[summaries.go](../../internal/service/source/summaries.go) summarizes active posts in the background, backfilled or not.`
- Step 6: `The pipeline skips posts already stored, like any other source, and inserts the rest as pending for the [quality gate](quality-gate.md).`
- Step 7: `RunSummaries summarizes 20 active posts a minute while DeepSeek is off peak. Until then a post shows its excerpt.`
- Under the schema block, add: `` `summary_pending` is replaced by `summary_checked`, see [quality gate](quality-gate.md). ``

- [ ] **Step 4: Update trust model**

In `trust-model.md`, change the Quality check row to:

```markdown
| Quality check | Quality gate on every post, shadow gate, demoted on drift | Hard gate on every post |
```

Add `[quality-gate](quality-gate.md)` to its `**Related:**` line.

- [ ] **Step 5: Commit**

```bash
git add docs/kb
git commit -m "Document the quality gate as built"
```

---

## Follow-up after deploy

Not part of this plan. Once the new binary runs in prod, add `internal/migrations/003_drop_summary_pending.sql` in its own push:

```sql
DROP INDEX IF EXISTS posts_summary_pending_idx;
ALTER TABLE posts DROP COLUMN IF EXISTS summary_pending;
```
