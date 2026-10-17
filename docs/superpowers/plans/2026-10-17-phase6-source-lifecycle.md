# Phase 6: Source Promotion, Dormancy and Probe Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the source lifecycle: proposed sources earn promotion to active while `source_promotion` is on, flagged discovered sources fall back to proposed, silent or failing sources go dormant, and dormant sources are probed weekly and revive on new posts.

**Architecture:** Sources get `status_changed_at`, `fetch_failures` and `promoted_at`. The source worker counts listing fetch failures, schedules dormant sources at the probe interval and revives a dormant source on its first new post. A daily lifecycle job in the `source` package marks dormant sources and, behind the switch, promotes qualifying proposed sources after finding an RSS feed on their homepage. Tag maintenance demotes active discovered sources when it flags them. Admin shows promotion readiness.

**Tech Stack:** Go 1.27, pgx v5, Postgres 18, goquery, SvelteKit admin with postgres.js.

**Spec:** `docs/kb/source-lifecycle.md` (steps 2 to 7), `docs/kb/rollout.md` (phase 6, `source_promotion`).

## Global Constraints

- States: `proposed` (not scraped, untrusted), `active` (scraped on `rate`), `dormant` (probe only, trusted). A source is scraped when `enabled = true` and `status = 'active'`. The lifecycle never changes `enabled`.
- Promotion runs only while `source_promotion` is on. Dormancy and probe always run.
- Promotion thresholds from `link_queue` rows of the source's `domain_id`: at least 5 accepted posts, on at least 3 distinct days, acceptance rate at least 60%. At most 5 promotions per week, oldest qualifying first.
- On promotion fetch the homepage. An RSS autodiscovery `<link rel="alternate" type="application/rss+xml">` gives `adapter = 'rss'` and the feed URL, otherwise `adapter = 'page'` with the homepage URL.
- Demote: when the shadow gate flags an active discovered source, set `status = 'proposed'`. A flagged manual source stays active.
- Dormancy: no new posts inside 30 days, or 10 consecutive fetch failures. `fetch_failures` resets on a successful fetch.
- Probe: a dormant source is scraped once per 7 days. New posts move it back to `active`.
- Schema changes go straight into `internal/migrations/schema.sql`. No numbered migration file, no rename block. The switch row is inserted with `ON CONFLICT DO NOTHING`.
- Go style: `any` not `interface{}`, guard clauses, wrap leaf errors as `fmt.Errorf("err <action>: %w", err)`, optimistic assignment, comments only for gotchas.
- One commit per purpose. End each commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.
- Do not reset the dev database. Verify SQL on the throwaway database `ingest_check` built from `schema.sql`, then drop it. Never print `.env` values.
- Temporary `check_test.go` files run against the throwaway database and are deleted before the commit.
- `.svelte` edits go through the Svelte MCP `svelte-autofixer` until it reports no issues. Never use npx, use pnpm.

## Plan rulings against the spec

These fill gaps the spec leaves open. Task 7 writes them back into the kb.

- **Promotion evidence counts only `link_queue` rows checked since `status_changed_at`.** Otherwise a demoted source would be promoted again the next day on the evidence that got it promoted before. The row that proposed the source has the same timestamp, so the comparison is `>=`.
- **The acceptance rate is accepted over accepted plus rejected.** `failed` and `duplicate` are no gate decisions.
- **Distinct days are calendar days of `checked_at` of accepted rows**, in the database time zone.
- **`promoted_at` records promotions** so the weekly cap counts promotions only, not probe revivals, which also set `status_changed_at`.
- **Only enabled, not deleted proposed sources are promoted.** You disable a proposed source to stop it.
- **Promotion clears `flagged_at` and sets `reviewed_at = now()`.** The shadow gate then judges the promoted source on its new posts only, so a demoted source that earns promotion again can be flagged again.
- **A homepage fetch failure skips the source** until the next daily run. It does not use up the weekly cap.
- **Atom autodiscovery counts as RSS.** The rss adapter parses Atom too. The first matching `<link>` wins, its `href` resolved against the final homepage URL.
- **The page adapter keeps the source URL**, the domain base URL, so a homepage redirect to another host cannot move the source.
- **Dormancy measures from the later of the last post and `status_changed_at`**, so a newly promoted or revived source gets the full 30 days. Only enabled sources go dormant.
- **Ten failures make a source dormant at once** inside the failure update, not at the next daily run.
- **The probe schedule is in memory** like the `rate` schedule. A restart probes every dormant source once.
- **Revival happens on the insert of a new post** from a dormant source, so the probe needs no separate result handling.
- **Demotion happens in the same update that flags**, so only a fresh flag demotes.

## Review Focus

- A demoted source must not be promoted again on old evidence. Pinned in Task 4 (source `d`).
- One news burst of accepted posts on one day must not promote. Pinned in Task 4 (source `b`).
- The weekly cap must count promotions, not revivals. Pinned in Task 4 (`promoted_at` row and revived source).
- A failing manual source must go dormant after 10 failures and reset its count on success. Pinned in Task 1.
- A flagged manual source must stay active while a flagged discovered one is demoted. Pinned in Task 5.

---

### Task 1: Lifecycle columns, fetch failures and revival

**Files:**
- Modify: `internal/migrations/schema.sql`, `internal/service/switches/store.go`, `internal/service/source/model.go`, `internal/service/source/store.go`, `internal/service/source/sourced.go`
- Temporary: `internal/service/source/check_test.go`

**Interfaces:**
- Produces: `sources.status_changed_at TIMESTAMPTZ NOT NULL DEFAULT now()`, `sources.fetch_failures INT NOT NULL DEFAULT 0`, `sources.promoted_at TIMESTAMPTZ`, switch row `source_promotion`, `switches.SourcePromotion`. `source.StatusDormant`, `source.StatusProposed`. `(*Store).RecordFetch(ctx, id string, ok bool, maxFailures int) error`, `(*Store).Revive(ctx, id string) error`. `maxFetchFailures = 10`, `probeInterval = 7 * 24 * time.Hour`.

- [ ] **Step 1: Schema and switch**

In `schema.sql`, in `sources` after `status TEXT NOT NULL DEFAULT 'active', -- proposed | active | dormant` add:

```sql
  status_changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  fetch_failures INT NOT NULL DEFAULT 0, -- consecutive listing fetch failures
  promoted_at TIMESTAMPTZ,
```

Change the switch insert to `('shadow_gate'), ('discovered_ingest'), ('search_discovery'), ('source_promotion')`. In `switches/store.go` add `SourcePromotion = "source_promotion"`.

In `source/model.go` replace `const StatusActive = "active"` with:

```go
const (
	StatusProposed = "proposed"
	StatusActive   = "active"
	StatusDormant  = "dormant"
)
```

- [ ] **Step 2: Write the temporary check**

Create `internal/service/source/check_test.go` with a `checkDB` helper reading `INGEST_CHECK_CONN` (same as phase 5) and:

```go
func TestFetchCheck(t *testing.T) {
	ctx := context.Background()
	db := checkDB(t)

	if _, err := db.Exec(ctx, `
		INSERT INTO domains (id, base_url) VALUES ('00000000-0000-0000-0000-000000000001', 'https://own.example');
		INSERT INTO sources (id, domain_id, adapter, url, enabled, status, fetch_failures) VALUES
			('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'rss', 'https://own.example/a', true, 'active', 8),
			('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000001', 'rss', 'https://own.example/b', true, 'dormant', 0),
			('00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000001', 'rss', 'https://own.example/c', true, 'proposed', 0);
		`); err != nil {
		t.Fatal(err)
	}

	s := NewStore(db, feed.NewStore(db), domain.NewStore(db))
	a, b, c := "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003", "00000000-0000-0000-0000-000000000004"

	state := func(id string) (status string, failures int) {
		t.Helper()

		if err := db.QueryRow(ctx, `SELECT status, fetch_failures FROM sources WHERE id = $1`, id).Scan(&status, &failures); err != nil {
			t.Fatal(err)
		}

		return
	}

	if err := s.RecordFetch(ctx, a, false, 10); err != nil {
		t.Fatal(err)
	}

	if st, n := state(a); st != "active" || n != 9 {
		t.Errorf("after 9 failures %s %d", st, n)
	}

	if err := s.RecordFetch(ctx, a, false, 10); err != nil {
		t.Fatal(err)
	}

	if st, n := state(a); st != "dormant" || n != 10 {
		t.Errorf("after 10 failures %s %d", st, n)
	}

	if err := s.RecordFetch(ctx, a, true, 10); err != nil {
		t.Fatal(err)
	}

	if st, n := state(a); st != "dormant" || n != 0 {
		t.Errorf("after success %s %d", st, n)
	}

	for _, id := range []string{b, c} {
		if err := s.Revive(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	if st, _ := state(b); st != "active" {
		t.Errorf("dormant after post %s", st)
	}

	if st, _ := state(c); st != "proposed" {
		t.Errorf("proposed after post %s", st)
	}

	sources, err := enabledSources(ctx, &s)
	if err != nil {
		t.Fatal(err)
	}

	rates := make(map[string]int)

	for _, src := range sources {
		rates[src.ID] = src.Rate
	}

	if len(rates) != 2 || rates[a] != int(probeInterval/time.Minute) || rates[b] != 15 {
		t.Errorf("scheduled %v", rates)
	}
}
```

Imports: `context`, `os`, `testing`, `time`, `internal/lib/database`, `internal/service/domain`, `internal/service/feed`.

- [ ] **Step 3: Run it to verify it fails**

Run: `go vet ./internal/service/source/`
Expected: FAIL, `s.RecordFetch undefined`.

- [ ] **Step 4: Store**

In `source/store.go` add:

```go
func (s *Store) RecordFetch(ctx context.Context, id string, ok bool, maxFailures int) error {
	if ok {
		if _, err := s.db.Exec(ctx, `UPDATE sources SET fetch_failures = 0 WHERE id = $1 AND fetch_failures > 0`, id); err != nil {
			return fmt.Errorf("err reset fetch failures: %w", err)
		}

		return nil
	}

	if _, err := s.db.Exec(ctx, `
		UPDATE sources
		SET
			fetch_failures = fetch_failures + 1,
			status = CASE WHEN status = 'active' AND fetch_failures + 1 >= $2 THEN 'dormant' ELSE status END,
			status_changed_at = CASE WHEN status = 'active' AND fetch_failures + 1 >= $2 THEN now() ELSE status_changed_at END
		WHERE id = $1
		`, id, maxFailures); err != nil {
		return fmt.Errorf("err record fetch failure: %w", err)
	}

	return nil
}

func (s *Store) Revive(ctx context.Context, id string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE sources SET status = 'active', status_changed_at = now()
		WHERE id = $1 AND status = 'dormant'
		`, id); err != nil {
		return fmt.Errorf("err revive source: %w", err)
	}

	return nil
}
```

- [ ] **Step 5: Worker**

In `sourced.go` add to the const block:

```go
	maxFetchFailures = 10
	probeInterval    = 7 * 24 * time.Hour
```

Replace `enabledSources` with:

```go
func enabledSources(ctx context.Context, s *Store) ([]Source, error) {
	var sources []Source

	for _, status := range []string{StatusActive, StatusDormant} {
		for offset := 0; ; offset += sourcePage {
			page, err := iterx.Collect(s.ListSources(ctx, Filter{
				Limit:   strconv.Itoa(sourcePage),
				Offset:  strconv.Itoa(offset),
				OrderBy: "id",
				Enabled: sql.Null[bool]{V: true, Valid: true},
				Status:  sql.Null[string]{V: status, Valid: true},
			}))
			if err != nil {
				return nil, err
			}

			for _, source := range page {
				if source.Status == StatusDormant {
					source.Rate = int(probeInterval / time.Minute)
				}

				sources = append(sources, source)
			}

			if len(page) < sourcePage {
				break
			}
		}
	}

	return sources, nil
}
```

In `fetchFromSource`, record the result:

```go
		for source := range in {
			dataset, err := p.FetchFromSource(ctx, source.URL)
			if rerr := p.store.RecordFetch(ctx, source.ID, err == nil, maxFetchFailures); rerr != nil {
				l.Error("record fetch", "source", source.ID, "err", rerr)
			}

			if err != nil {
				l.Error("fetchFromSource", "source", source.ID, "url", source.URL, "err", err)
				continue
			}
```

In `Run` replace the `CreateFeed` loop body with:

```go
	for f := range merge(ctx, feeds...) {
		_, err := s.feedStore.CreateFeed(ctx, f)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			l.Error("create feed", "source", f.SourceID, "url", f.URL, "err", err)
			continue
		}

		if err := s.Revive(ctx, f.SourceID); err != nil {
			l.Error("revive source", "source", f.SourceID, "err", err)
		}
	}
```

- [ ] **Step 6: Run the check**

Build `ingest_check` from `schema.sql`, run `go test ./internal/service/source/ -run Check -count=1 -v` with `INGEST_CHECK_CONN` from `.env`, drop the database.
Expected: `PASS`.

- [ ] **Step 7: Commit**

```bash
rm internal/service/source/check_test.go
gofmt -l internal; go build ./... && go vet ./... && go test ./...
git add internal/migrations/schema.sql internal/service/switches/store.go internal/service/source/model.go internal/service/source/store.go internal/service/source/sourced.go
git commit -m "Track source fetch failures, probe dormant sources and revive them on new posts"
```

---

### Task 2: Dormancy

**Files:**
- Create: `internal/service/source/lifecycle.go`
- Modify: `internal/service/source/store.go`, `cmd/main.go`
- Temporary: `internal/service/source/check_test.go`

**Interfaces:**
- Produces: `(*Store).MarkDormant(ctx, windowDays int) (int64, error)`, `source.RunLifecycle(ctx, s *Store, sw *switches.Store, l *slog.Logger) error`, `lifecycle(ctx, s, sw, client, l) error` (Task 4 extends it), `dormancyWindow = 30`.

- [ ] **Step 1: Write the temporary check**

`TestDormancyCheck`: insert four enabled `active` sources and one disabled one:
- `a`: `status_changed_at` 40 days ago, a post created 40 days ago → dormant.
- `b`: `status_changed_at` 40 days ago, a post created 2 days ago → stays active.
- `c`: `status_changed_at` 2 days ago, no posts → stays active.
- `d`: `status_changed_at` 40 days ago, no posts → dormant.
- `e`: `enabled = false`, `status_changed_at` 40 days ago, no posts → stays active.

Call `s.MarkDormant(ctx, 30)`. Expect 2 rows affected, `a` and `d` dormant with `status_changed_at` within the last minute.

- [ ] **Step 2: Run it to verify it fails**

Run: `go vet ./internal/service/source/`
Expected: FAIL, `s.MarkDormant undefined`.

- [ ] **Step 3: Store**

```go
func (s *Store) MarkDormant(ctx context.Context, windowDays int) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE sources s
		SET status = 'dormant', status_changed_at = now()
		WHERE s.status = 'active'
			AND s.enabled
			AND s.deleted_at IS NULL
			AND greatest(s.status_changed_at, (SELECT max(p.created_at) FROM posts p WHERE p.source_id = s.id)) < now() - make_interval(days => $1)
		`, windowDays)
	if err != nil {
		return 0, fmt.Errorf("err mark dormant sources: %w", err)
	}

	return tag.RowsAffected(), nil
}
```

- [ ] **Step 4: Job**

Create `internal/service/source/lifecycle.go`:

```go
package source

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/switches"
)

const (
	lifecycleInterval = 24 * time.Hour
	dormancyWindow    = 30
	homepageTimeout   = 20 * time.Second
)

func RunLifecycle(ctx context.Context, s *Store, sw *switches.Store, l *slog.Logger) error {
	client := &http.Client{Timeout: homepageTimeout}

	for range schedule.Always.Tick(ctx, lifecycleInterval) {
		if err := lifecycle(ctx, s, sw, client, l); err != nil {
			l.Error("source lifecycle", "err", err)
		}
	}

	return ctx.Err()
}

func lifecycle(ctx context.Context, s *Store, sw *switches.Store, client *http.Client, l *slog.Logger) error {
	dormant, err := s.MarkDormant(ctx, dormancyWindow)
	if err != nil {
		return err
	}

	l.Info("source lifecycle", "dormant", dormant)

	return nil
}
```

In `cmd/main.go` add after the sources `g.Go` block:

```go
	g.Go(func() error {
		return source.RunLifecycle(ctx, &sourceStore, &switchStore, l.WithGroup("lifecycle"))
	})
```

- [ ] **Step 5: Run the check, delete it, build, commit** `Put silent sources to sleep`.

---

### Task 3: Listing discovery

**Files:**
- Create: `internal/service/source/listing.go`, `internal/service/source/listing_test.go`

**Interfaces:**
- Produces: `findListing(page []byte, pageURL, sourceURL string) (adapterName, url string)`.

- [ ] **Step 1: Write the failing test**

```go
package source

import "testing"

func TestFindListing(t *testing.T) {
	cases := []struct {
		name, page, adapter, url string
	}{
		{"rss", `<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`, "rss", "https://www.a.example/feed.xml"},
		{"atom", `<html><head><link rel="alternate" type="application/atom+xml" href="https://feeds.example/a"></head></html>`, "rss", "https://feeds.example/a"},
		{"first wins", `<html><head><link rel="alternate" type="application/rss+xml" href="/one"><link rel="alternate" type="application/rss+xml" href="/two"></head></html>`, "rss", "https://www.a.example/one"},
		{"rel list", `<html><head><link rel="alternate nofollow" type="application/rss+xml" href="/f"></head></html>`, "rss", "https://www.a.example/f"},
		{"other alternate", `<html><head><link rel="alternate" hreflang="en" href="/en"></head></html>`, "page", "https://a.example"},
		{"empty href", `<html><head><link rel="alternate" type="application/rss+xml" href=""></head></html>`, "page", "https://a.example"},
		{"none", `<html><body>Hej</body></html>`, "page", "https://a.example"},
	}

	for _, c := range cases {
		adapter, url := findListing([]byte(c.page), "https://www.a.example/", "https://a.example")
		if adapter != c.adapter || url != c.url {
			t.Errorf("%s: got %s %s", c.name, adapter, url)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails** (`undefined: findListing`).

- [ ] **Step 3: Implement**

```go
package source

import (
	"bytes"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func findListing(page []byte, pageURL, sourceURL string) (adapterName, listing string) {
	adapterName, listing = "page", sourceURL

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return
	}

	doc.Find(`link[rel~="alternate"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		typ := strings.ToLower(strings.TrimSpace(s.AttrOr("type", "")))
		href := strings.TrimSpace(s.AttrOr("href", ""))

		if (typ != "application/rss+xml" && typ != "application/atom+xml") || href == "" {
			return true
		}

		ref, err := url.Parse(href)
		if err != nil {
			return true
		}

		adapterName, listing = "rss", base.ResolveReference(ref).String()

		return false
	})

	return
}
```

- [ ] **Step 4: Run tests, commit** `Find a feed on a source homepage`.

---

### Task 4: Promotion

**Files:**
- Modify: `internal/service/source/store.go`, `internal/service/source/lifecycle.go`
- Temporary: `internal/service/source/check_test.go`

**Interfaces:**
- Consumes: `findListing` (Task 3), `adapter.FetchPage`, `switches.SourcePromotion`.
- Produces: `Promotable{ID, URL string}`, `(*Store).PromotedLastWeek(ctx) (int, error)`, `(*Store).ListPromotable(ctx, minAccepted, minDays int, minRate float64, limit int) ([]Promotable, error)`, `(*Store).Promote(ctx, id, adapterName, url string) error`. Constants `minAccepted = 5`, `minDays = 3`, `minAcceptRate = 0.6`, `maxPromotionsPerWeek = 5`.

- [ ] **Step 1: Write the temporary check**

`TestPromotionCheck`. An `httptest` server serves `/` with an RSS `<link>` to `/feed.xml`. Domains and enabled `proposed` sources, `status_changed_at` 30 days ago unless noted, `created_at` ascending in this order:
- `a`: url = server URL. 5 accepted rows on 3 distinct days (now - 1, 2, 3 days), 2 rejected → qualifies (5/7 = 71%).
- `b`: 6 accepted rows all on one day → not promoted (1 day).
- `c`: 5 accepted on 3 days, 4 rejected → not promoted (5/9 = 56%).
- `d`: `status_changed_at` = now - 1 hour, its 5 accepted rows on 3 days are older → not promoted (old evidence).
- `e`: `enabled = false`, qualifying rows → not promoted.
- `f`: url `http://127.0.0.1:1` (refused), qualifying rows → skipped, stays proposed.

Plus an active discovered source `r` with `status_changed_at` now and `promoted_at` NULL (a revival) and one with `promoted_at` 2 days ago.

Run `lifecycle` with the switch off: nothing promoted. Turn `source_promotion` on and run again. Expect: `a` active, `adapter = 'rss'`, `url = <server>/feed.xml`, `promoted_at` set, `reviewed_at` set, `flagged_at` NULL. `b`, `c`, `d`, `e`, `f` still proposed. `PromotedLastWeek` = 2.

Then set `promoted_at = now()` on 3 more rows of other sources and add a qualifying source `g`. Run again: `g` stays proposed (cap 5 reached).

- [ ] **Step 2: Run it to verify it fails** (`undefined: Promotable` or missing methods).

- [ ] **Step 3: Store**

```go
type Promotable struct {
	ID  string
	URL string
}

func (s *Store) PromotedLastWeek(ctx context.Context) (n int, err error) {
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM sources WHERE promoted_at > now() - interval '7 days'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count promotions: %w", err)
	}

	return n, nil
}

func (s *Store) ListPromotable(ctx context.Context, minAccepted, minDays int, minRate float64, limit int) ([]Promotable, error) {
	rows, err := s.db.Query(ctx, `
		SELECT s.id, s.url
		FROM sources s
		JOIN LATERAL (
			SELECT
				count(*) FILTER (WHERE q.status = 'accepted') AS accepted,
				count(DISTINCT q.checked_at::date) FILTER (WHERE q.status = 'accepted') AS days,
				count(*) FILTER (WHERE q.status IN ('accepted', 'rejected')) AS decided
			FROM link_queue q
			WHERE q.domain_id = s.domain_id AND q.checked_at >= s.status_changed_at
		) e ON true
		WHERE s.status = 'proposed'
			AND s.enabled
			AND s.deleted_at IS NULL
			AND e.accepted >= $1
			AND e.days >= $2
			AND e.accepted >= $3 * e.decided
		ORDER BY s.created_at
		LIMIT $4
		`, minAccepted, minDays, minRate, limit)
	if err != nil {
		return nil, fmt.Errorf("err query promotable sources: %w", err)
	}

	sources, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (p Promotable, err error) {
		err = row.Scan(&p.ID, &p.URL)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan promotable sources: %w", err)
	}

	return sources, nil
}

func (s *Store) Promote(ctx context.Context, id, adapterName, url string) error {
	// reviewed_at makes the shadow gate judge the source on posts after promotion only.
	if _, err := s.db.Exec(ctx, `
		UPDATE sources
		SET status = 'active', adapter = $2, url = $3, status_changed_at = now(), promoted_at = now(),
			flagged_at = NULL, reviewed_at = now(), fetch_failures = 0
		WHERE id = $1 AND status = 'proposed'
		`, id, adapterName, url); err != nil {
		return fmt.Errorf("err promote source: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Job**

Add to the lifecycle const block `minAccepted = 5`, `minDays = 3`, `minAcceptRate = 0.6`, `maxPromotionsPerWeek = 5`. Replace `lifecycle` with:

```go
func lifecycle(ctx context.Context, s *Store, sw *switches.Store, client *http.Client, l *slog.Logger) error {
	dormant, err := s.MarkDormant(ctx, dormancyWindow)
	if err != nil {
		return err
	}

	promoted, err := promote(ctx, s, sw, client, l)

	l.Info("source lifecycle", "dormant", dormant, "promoted", promoted)

	return err
}

func promote(ctx context.Context, s *Store, sw *switches.Store, client *http.Client, l *slog.Logger) (promoted int, err error) {
	on, err := sw.Enabled(ctx, switches.SourcePromotion)
	if err != nil || !on {
		return 0, err
	}

	done, err := s.PromotedLastWeek(ctx)
	if err != nil || done >= maxPromotionsPerWeek {
		return 0, err
	}

	// Sources whose homepage fails are skipped, so list more than the cap allows.
	candidates, err := s.ListPromotable(ctx, minAccepted, minDays, minAcceptRate, sourcePage)
	if err != nil {
		return 0, err
	}

	for _, c := range candidates {
		if done+promoted >= maxPromotionsPerWeek {
			break
		}

		page, final, err := adapter.FetchPage(ctx, client, c.URL)
		if err != nil {
			l.Warn("fetch homepage", "source", c.ID, "url", c.URL, "err", err)
			continue
		}

		name, listing := findListing(page, final, c.URL)

		if err := s.Promote(ctx, c.ID, name, listing); err != nil {
			return promoted, err
		}

		promoted++
	}

	return promoted, nil
}
```

Import `github.com/dagulv/kharoborathus/internal/service/source/adapter`.

- [ ] **Step 5: Run the check, delete it, build, commit** `Promote proposed sources that keep passing the gate`.

---

### Task 5: Demotion

**Files:**
- Modify: `internal/service/tagging/store.go` (`FlagSources`)
- Temporary: `internal/service/tagging/check_test.go`

- [ ] **Step 1: Write the temporary check**

`TestDemoteCheck`: three enabled sources, each with 50 posts whose `tag_checked.gate_passed` is false: manual `active` source `m`, discovered `active` source `d`, discovered `dormant` source `z`. Call `FlagSources(ctx, 50, 0.7)`. Expect 3 flagged, `m` active, `d` proposed with `status_changed_at` within the last minute, `z` dormant.

- [ ] **Step 2: Run it to verify it fails** (`d` still active).

- [ ] **Step 3: Implement**

In `FlagSources` change `SET flagged_at = now()` to:

```sql
		SET
			flagged_at = now(),
			status = CASE WHEN s.origin = 'discovered' AND s.status = 'active' THEN 'proposed' ELSE s.status END,
			status_changed_at = CASE WHEN s.origin = 'discovered' AND s.status = 'active' THEN now() ELSE s.status_changed_at END
```

- [ ] **Step 4: Run the check, delete it, build, commit** `Demote flagged discovered sources to proposed`.

---

### Task 6: Admin

**Files:**
- Modify: `frontend/src/routes/admin/admin.remote.ts`, `frontend/src/routes/admin/switches/+page.svelte`

- [ ] **Step 1: Readiness**

In `listSwitches` add:

```sql
				WHEN 'source_promotion' THEN (
					SELECT coalesce(extract(day FROM now() - min(created_at))::int, 0) || ' days of link queue history, '
						|| (
							SELECT count(*)
							FROM sources s
							JOIN LATERAL (
								SELECT
									count(*) FILTER (WHERE q.status = 'accepted') AS accepted,
									count(DISTINCT q.checked_at::date) FILTER (WHERE q.status = 'accepted') AS days,
									count(*) FILTER (WHERE q.status IN ('accepted', 'rejected')) AS decided
								FROM link_queue q
								WHERE q.domain_id = s.domain_id AND q.checked_at >= s.status_changed_at
							) e ON true
							WHERE s.status = 'proposed' AND s.enabled AND s.deleted_at IS NULL
								AND e.accepted >= 5 AND e.days >= 3 AND e.accepted >= 0.6 * e.decided
						) || ' proposed sources qualify'
					FROM link_queue
				)
```

with a comment above `listSwitches` already pointing at rollout; add `// source_promotion thresholds mirror source/lifecycle.go`.

- [ ] **Step 2: Description**

`source_promotion: 'Promotes proposed sources with at least 5 accepted posts on 3 days and a 60% acceptance rate, at most 5 a week. Dormancy and probe run either way.'`

- [ ] **Step 3: Autofix, check, lint, verify the SQL on `ingest_check`** (expected `0 days of link queue history, 0 proposed sources qualify`), commit `Show source promotion readiness in admin`.

---

### Task 7: Knowledge base

- `source-lifecycle.md`: status `done`. Current state describes `lifecycle.go`, `RecordFetch`, `Revive`, probe scheduling and demotion in `FlagSources`. Add the plan rulings as Rules. Replace the Schema changes block with the three new columns and drop the migration-file paragraph, since schema changes go straight into `schema.sql`.
- `rollout.md`: status `done`, current state `All six phases are done. Every switch starts off.`
- `architecture.md`: add a Lifecycle row to the worker table.
- `index.md`: source lifecycle and rollout `done`.
- Commit `Describe the source lifecycle in kb`.
