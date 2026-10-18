# Phase 4: Link Extraction and Discovered Ingest Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fill `link_events` with outbound links from trusted posts, and while the `discovered_ingest` switch is on, turn queued URLs into gated posts whose new domains become proposed sources.

**Architecture:** Sources get `origin` and `status`, and only `active` sources are scraped. A new `ingest` package holds two workers. Link extraction parses markdown links from recent trusted posts into `link_events` with no AI. The ingester fetches pending links one host per tick, checks `robots.txt`, trims, rejects duplicates by simhash, runs the hard gate (the Jev tagging call plus one article question) and only then summarizes and inserts the post, its source, its `discovered` link and its `tag_checked` row in one transaction. Admin shows source status, link queue counts and the switch readiness.

**Tech Stack:** Go 1.27, pgx v5, Postgres 18, SvelteKit admin with postgres.js and valibot.

**Spec:** `docs/kb/link-extraction.md`, `docs/kb/discovered-ingest.md`, `docs/kb/trust-model.md`, `docs/kb/source-lifecycle.md` (step 1 Propose only), `docs/kb/classifier-gate.md` (hard gate), `docs/kb/post-linking.md` (`discovered`), `docs/kb/rollout.md` (phase 4).

## Global Constraints

- Trusted means source `status` is `active` or `dormant`. `proposed` is untrusted. Trust is read through `posts.source_id`.
- A source is scraped only when `enabled = true` and `status = 'active'`. The lifecycle never changes `enabled`.
- Link extraction runs whether or not the switch is on. The ingester reads `discovered_ingest` at the start of every tick and skips the tick when off.
- Link extraction: strip `utm_*` parameters and fragments. Skip links already in `posts` or `link_events`, links to the post's own domain, blocklisted social, video and file hosts, and anything not http or https. Max 10 links per post. `via = 'link'`.
- Ingest defaults: daily fetch cap 200 URLs, per-domain rate 1 request per 10 seconds, max attempts 3 then `failed`, pending window 7 days.
- `link_events.status`: `pending | accepted | rejected | duplicate | failed`.
- Hard gate: accept if the article question and any tag with `gate = true` score at least 0.8, the existing `tagThreshold`. Store the scores in `gate_scores`.
- A proposed source has `origin = 'discovered'`, `status = 'proposed'`, `enabled = true` (set explicitly) and `url` set to the domain's base URL.
- A `discovered` link points from the found post to the post whose link led to it, with metadata `via`.
- Untrusted posts never propose tags. Accepted posts are marked `tag_checked`, so the tagging worker skips them.
- Phase 4 adds only `origin` and `status` to sources. `status_changed_at` and `fetch_failures` belong to phase 6.
- Schema changes go straight into `internal/migrations/schema.sql`. No numbered migration file. The switch row is inserted with `ON CONFLICT DO NOTHING`.
- Go style: `any` not `interface{}`, guard clauses, wrap leaf errors as `fmt.Errorf("err <action>: %w", err)`, optimistic assignment, comments only for gotchas.
- One commit per purpose. End each commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.
- Do not reset the dev database. Verify SQL on the throwaway database `ingest_check` built from `schema.sql`, then drop it. Never print `.env` values.
- Temporary `check_test.go` files run against the throwaway database and are deleted before the commit. They are never committed.
- `.svelte` edits go through the Svelte MCP `svelte-autofixer` until it reports no issues. Never use npx, use pnpm.

## Plan rulings against the spec

These fill gaps the spec leaves open. Task 10 writes them back into the kb.

- **Extraction scans only posts created inside the 7-day pending window.** Otherwise the first run queues links from every old post with a fresh `created_at`, and the ingester fetches them as if they were new.
- **Image links are not links.** `![alt](src)` is skipped. A linked image `[![alt](img)](href)` is skipped too, since the parser only reads link text without brackets.
- **Own domain compares hosts**, lowercased, without `www.` and scheme, so `http://www.a.example` and `https://a.example` match.
- **One link per host per tick**, oldest first and fresh links before retries. The 10-second rate then only applies between the `robots.txt` fetch and the page fetch of the same host, which the pacer enforces.
- **The daily cap is a rolling 24 hours** counted from `link_events.checked_at`.
- **`robots.txt` disallow sets `failed`** with error `disallowed by robots.txt`, without retries. It is no gate decision, so it must not count as `rejected` in the acceptance rate of phase 6. A missing `robots.txt` (4xx) allows everything. 5xx, 429 and network errors are retried like a failed fetch. Results are cached per host for 24 hours.
- **No gate tag means no tick.** With no enabled, active tag with `gate = true` every link would be rejected for good, so the ingester waits.
- **Classifier unavailable stops the tick** and leaves the link untouched, like the tagging worker.
- **A URL conflict on insert sets `duplicate`** with error `url exists`. A trusted source may insert the same URL between queueing and ingest.
- **Short content and failed trimmer checks are one check.** `Trim.Apply` leaves `Content` empty when the extraction fails any check, including `too_short`. Both reject with error `no content`. A missing title rejects with `no title`.
- **The domain's oldest source includes deleted ones.** "Whatever its status" is read literally. Posts on a domain whose source you deleted attach to it instead of proposing it again.
- **`source_discovered_by_post` records the referring post** (`from_post_id`), once, when the proposed source is created.
- **Discovered posts do not update `tags.last_matched_at`**, and tag rates and the broadness sample count only posts from trusted sources. Untrusted posts all passed the gate, so counting them makes gate tags look broad.
- **Manual sync of a non-active source returns 409.** Admin hides the Sync button for them.
- **Language comes from `<html lang>`** of the fetched page, like the page adapter.

## Review Focus

- A post linking to its own site with `www.` or another scheme, to an image, a relative path or `mailto:` must queue nothing for those links. Pinned in Task 3.
- Links in old posts, or rows queued more than 7 days ago, must never be fetched. Pinned in Task 4 (post `p3`) and Task 8 (`/news/old`).
- A URL that turns into a post through a trusted source before ingest must end `duplicate`, not loop on retries. Pinned in Task 8 (`/news/ericsson`).
- A host that 404s or disallows the path must stop after 3 attempts or at once, and never block other links. Pinned in Task 6 and Task 8 (`/missing`, `/private/x`).
- A proposed source must never be scraped and its posts must never shape tag rates. Pinned in Task 1 (enabled sources query) and Task 2.

---

### Task 1: Source origin and status

**Files:**

- Modify: `internal/migrations/schema.sql` (`sources`)
- Modify: `internal/service/source/model.go`, `internal/service/source/store.go`, `internal/service/source/sourced.go`, `internal/service/source/route.go`

**Interfaces:**

- Produces: `sources.origin TEXT NOT NULL DEFAULT 'manual'`, `sources.status TEXT NOT NULL DEFAULT 'active'`. `source.Source.Status`, `source.Source.Origin`, `source.Filter.Status sql.Null[string]`. Task 2, 4, 8 and 9 read `status`.

- [ ] **Step 1: Add the columns**

In `internal/migrations/schema.sql`, in `CREATE TABLE IF NOT EXISTS sources`, after `target_tags TEXT[] NOT NULL DEFAULT '{}',` add:

```sql
  origin TEXT NOT NULL DEFAULT 'manual', -- manual | discovered
  status TEXT NOT NULL DEFAULT 'active', -- proposed | active | dormant
```

- [ ] **Step 2: Verify the schema builds and the enabled sources query on a throwaway database**

Write `$SCRATCHPAD/source_check.sql`:

```sql
INSERT INTO domains (id, base_url) VALUES ('00000000-0000-0000-0000-000000000001', 'https://a.example');
INSERT INTO sources (domain_id, adapter, url, enabled) VALUES ('00000000-0000-0000-0000-000000000001', 'rss', 'https://a.example/rss', true);
INSERT INTO sources (domain_id, adapter, url, enabled, origin, status) VALUES ('00000000-0000-0000-0000-000000000001', 'page', 'https://a.example', true, 'discovered', 'proposed');
SELECT url, origin, status FROM sources WHERE deleted_at IS NULL AND enabled = true AND status = 'active';
```

Run:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" ingest_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d ingest_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d ingest_check' < "$SCRATCHPAD/source_check.sql"
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" ingest_check'
```

Expected: one row, `https://a.example/rss | manual | active`.

- [ ] **Step 3: Model and store**

In `internal/service/source/model.go` add to `Source`, after `Enabled`:

```go
	Status     string            `json:"status"` // proposed | active | dormant
	Origin     string            `json:"origin"` // manual | discovered
```

and to `Filter`:

```go
	Status  sql.Null[string] `json:"status"`
```

In `internal/service/source/store.go`:

- Add `status,` and `origin,` to `sourceColumns` right after `enabled,`.
- In `scanSource`, scan `&source.Status, &source.Origin,` right after `&source.Enabled,`.
- In `ListSources`, after the `filter.Enabled` block:

```go
		if filter.Status.Valid {
			args = append(args, filter.Status.V)
			where += fmt.Sprintf(` AND status = $%d`, len(args))
		}
```

Add to `model.go`:

```go
const StatusActive = "active"
```

- [ ] **Step 4: Scrape only active sources**

In `internal/service/source/sourced.go` `enabledSources`, add to the `Filter` literal:

```go
			Status:  sql.Null[string]{V: StatusActive, Valid: true},
```

- [ ] **Step 5: Reject manual sync of non-active sources**

In `internal/service/source/route.go` `syncSource`, after the `GetSource` error block and before the `select`:

```go
		if source.Status != StatusActive {
			http.Error(w, "source not active", http.StatusConflict)
			return
		}
```

- [ ] **Step 6: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all ok.

- [ ] **Step 7: Commit**

```bash
git add internal/migrations/schema.sql internal/service/source/model.go internal/service/source/store.go internal/service/source/sourced.go internal/service/source/route.go
git commit -m "Add source origin and status and scrape only active sources

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Tag rates from trusted posts only

**Files:**

- Modify: `internal/service/tagging/store.go` (`RecomputeRates`, `ListRecentPosts`)
- Temporary: `internal/service/tagging/check_test.go` (deleted before commit)

**Interfaces:**

- Consumes: `sources.status` from Task 1.
- Produces: unchanged signatures.

- [ ] **Step 1: Write the temporary check**

Create `internal/service/tagging/check_test.go`:

```go
package tagging

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/database"
)

func TestTrustedRatesCheck(t *testing.T) {
	conn := os.Getenv("INGEST_CHECK_CONN")
	if conn == "" {
		t.Skip("INGEST_CHECK_CONN not set")
	}

	ctx := context.Background()

	db, err := database.NewDatabase(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(ctx, `
		INSERT INTO domains (id, base_url) VALUES ('00000000-0000-0000-0000-000000000001', 'https://a.example');
		INSERT INTO sources (id, domain_id, adapter, url) VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'rss', 'https://a.example/rss');
		INSERT INTO sources (id, domain_id, adapter, url, origin, status) VALUES ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000001', 'page', 'https://a.example', 'discovered', 'proposed');
		INSERT INTO tags (key, created_at) VALUES ('k', now() - interval '60 days');
		INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, content_hash, tags)
		SELECT
			CASE WHEN i <= 20 THEN '00000000-0000-0000-0000-000000000002' ELSE '00000000-0000-0000-0000-000000000003' END::uuid,
			'00000000-0000-0000-0000-000000000001', 'https://a.example/' || i, 'sv', 't', '', '', '\x00',
			CASE WHEN i = 1 OR i > 20 THEN '{k}'::text[] ELSE '{}' END
		FROM generate_series(1, 50) i;
		INSERT INTO tag_checked (post_id) SELECT id FROM posts;
		`); err != nil {
		t.Fatal(err)
	}

	s := NewStore(db)

	if _, err := s.RecomputeRates(ctx, rateWindow, gateMaxRate, minSample); err != nil {
		t.Fatal(err)
	}

	var rate float64
	var gate bool

	if err := db.QueryRow(ctx, `SELECT match_rate, gate FROM tags WHERE key = 'k'`).Scan(&rate, &gate); err != nil {
		t.Fatal(err)
	}

	if math.Abs(rate-0.05) > 1e-6 || !gate {
		t.Errorf("rate %v gate %v", rate, gate)
	}

	posts, err := s.ListRecentPosts(ctx, sampleDays, sampleLimit)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range posts {
		if p.SourceID != "00000000-0000-0000-0000-000000000002" {
			t.Errorf("untrusted post %s in sample", p.ID)
		}
	}

	if len(posts) != 20 {
		t.Errorf("%d posts in sample", len(posts))
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" ingest_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d ingest_check' < internal/migrations/schema.sql
(set -a; . ./.env; INGEST_CHECK_CONN="postgresql://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:5432/ingest_check?sslmode=disable" go test ./internal/service/tagging/ -run Check -count=1)
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" ingest_check'
```

Expected: FAIL with `rate 0.62 gate false` and `50 posts in sample`.

- [ ] **Step 3: Count trusted posts only**

In `RecomputeRates`, replace the comment and the `FROM ... WHERE` lines of the `rates` CTE:

```go
	// Only posts classified after the tag existed count, otherwise new tags look rare and wrongly gate.
	// Untrusted posts all passed the gate, so counting them would make gate tags look broad.
```

```sql
			FROM tags t
			JOIN tag_checked c ON c.checked_at >= t.created_at
			JOIN posts p ON p.id = c.post_id
			JOIN sources s ON s.id = p.source_id
			WHERE t.status = 'active'
				AND p.created_at > now() - make_interval(days => $1)
				AND s.status IN ('active', 'dormant')
```

Replace the `ListRecentPosts` query with:

```sql
		SELECT p.id, p.source_id, p.lang, p.title, p.content
		FROM posts p
		JOIN sources s ON s.id = p.source_id
		WHERE p.created_at > now() - make_interval(days => $1)
			AND s.status IN ('active', 'dormant')
		ORDER BY p.created_at DESC
		LIMIT $2
```

- [ ] **Step 4: Run the check again**

Run the four commands of Step 2.
Expected: PASS.

- [ ] **Step 5: Delete the check, build and test**

```bash
rm internal/service/tagging/check_test.go
go build ./... && go vet ./... && go test ./...
```

Expected: all ok, and `git status --short` shows only `internal/service/tagging/store.go`.

- [ ] **Step 6: Commit**

```bash
git add internal/service/tagging/store.go
git commit -m "Rate tags over trusted posts only

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Outbound links

**Files:**

- Create: `internal/service/ingest/links.go`
- Test: `internal/service/ingest/links_test.go`

**Interfaces:**

- Produces: `func outboundLinks(markdown, ownBaseURL string) []string` and `func hostOf(rawURL string) string` in package `ingest`. Task 4 calls `outboundLinks`, Task 6 and 8 call `hostOf`.

- [ ] **Step 1: Write the failing test**

Create `internal/service/ingest/links_test.go`:

```go
package ingest

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestOutboundLinks(t *testing.T) {
	md := `Volvo [köper](https://news.example/a?utm_source=x&id=7#top) Northvolt.

![bild](https://img.example/b.jpg)

[Egen sida](https://own.example/om) och [www](http://www.own.example/x).

[Relativ](/c) [mejl](mailto:a@b.example) [ftp](ftp://files.example/f)

[Facebook](https://m.facebook.com/volvo) [Tube](https://youtu.be/x) [Drive](https://drive.google.com/file/d/1)

[Igen](https://news.example/a?id=7) [Titel](https://other.example/d "Rubrik")

[Spår](https://other.example/e?utm_medium=m)`

	got := outboundLinks(md, "https://www.own.example")
	want := []string{"https://news.example/a?id=7", "https://other.example/d", "https://other.example/e"}

	if !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestOutboundLinksCap(t *testing.T) {
	var b strings.Builder

	for i := range 15 {
		fmt.Fprintf(&b, "[l%d](https://news.example/%d) ", i, i)
	}

	got := outboundLinks(b.String(), "https://own.example")

	if len(got) != maxLinksPerPost || got[0] != "https://news.example/0" || got[9] != "https://news.example/9" {
		t.Errorf("got %v", got)
	}
}

func TestOutboundLinksNone(t *testing.T) {
	if got := outboundLinks("Inga länkar här.", "https://own.example"); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://WWW.A.example/x":   "a.example",
		"http://a.example:8080/x":   "a.example",
		"https://news.a.example":    "news.a.example",
		"not a url %zz":             "",
	}

	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/service/ingest/`
Expected: FAIL with `undefined: outboundLinks`.

- [ ] **Step 3: Implement**

Create `internal/service/ingest/links.go`:

```go
package ingest

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const maxLinksPerPost = 10

var markdownLink = regexp.MustCompile(`(!?)\[[^\[\]]*\]\(<?([^\s()<>]+)>?(?:\s+"[^"]*")?\)`)

var blockedHosts = []string{
	"facebook.com", "fb.com", "instagram.com", "threads.net", "twitter.com", "x.com", "t.co",
	"linkedin.com", "lnkd.in", "reddit.com", "pinterest.com", "bsky.app", "mastodon.social",
	"t.me", "whatsapp.com", "youtube.com", "youtu.be", "vimeo.com", "tiktok.com", "twitch.tv",
	"spotify.com", "soundcloud.com", "podcasts.apple.com", "apps.apple.com", "play.google.com",
	"drive.google.com", "docs.google.com", "dropbox.com", "wetransfer.com", "box.com",
	"onedrive.live.com", "1drv.ms", "issuu.com", "scribd.com", "slideshare.net",
	"flickr.com", "imgur.com", "giphy.com",
}

func outboundLinks(markdown, ownBaseURL string) []string {
	own := hostOf(ownBaseURL)

	var out []string

	for _, m := range markdownLink.FindAllStringSubmatch(markdown, -1) {
		if m[1] == "!" {
			continue
		}

		link, ok := normalizeLink(m[2])
		if !ok || slices.Contains(out, link) {
			continue
		}

		if host := hostOf(link); host == own || blocked(host) {
			continue
		}

		out = append(out, link)

		if len(out) == maxLinksPerPost {
			break
		}
	}

	return out
}

func normalizeLink(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}

	u.Fragment, u.RawFragment = "", ""

	q := u.Query()
	tracked := false

	for key := range q {
		if strings.HasPrefix(strings.ToLower(key), "utm_") {
			q.Del(key)
			tracked = true
		}
	}

	if tracked {
		u.RawQuery = q.Encode()
	}

	return u.String(), true
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

func blocked(host string) bool {
	return slices.ContainsFunc(blockedHosts, func(b string) bool {
		return host == b || strings.HasSuffix(host, "."+b)
	})
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `gofmt -l internal/service/ingest; go test ./internal/service/ingest/`
Expected: no files listed, `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/service/ingest/links.go internal/service/ingest/links_test.go
git commit -m "Parse outbound links from post markdown

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Link extraction worker

**Files:**

- Modify: `internal/migrations/schema.sql` (`link_events`, `link_checked`)
- Create: `internal/service/ingest/model.go`, `internal/service/ingest/store.go`, `internal/service/ingest/extract.go`
- Modify: `cmd/main.go`
- Temporary: `internal/service/ingest/check_test.go` (deleted before commit)

**Interfaces:**

- Consumes: `outboundLinks` from Task 3, `sources.status` from Task 1.
- Produces: tables `link_events` and `link_checked`. `ingest.Store` with `NewStore(db *database.Database) Store`, `ListUnscannedPosts(ctx, windowDays, limit int) ([]ScanPost, error)`, `QueueLinks(ctx, postID string, urls []string) (int64, error)`. `func RunExtraction(ctx context.Context, s *Store, l *slog.Logger) error`. Const `pendingWindow = 7`. Task 8 extends the same files.

- [ ] **Step 1: Add the tables**

In `internal/migrations/schema.sql`, after the `discover_checked` table, add:

```sql
CREATE TABLE IF NOT EXISTS link_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  url TEXT UNIQUE NOT NULL,
  via TEXT NOT NULL, -- link | search
  from_post_id UUID NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  discovery_events_id UUID REFERENCES discovery_events (id) ON DELETE SET NULL,
  domain_id UUID REFERENCES domains (id), -- set on fetch, drives source promotion
  status TEXT NOT NULL DEFAULT 'pending', -- pending | accepted | rejected | duplicate | failed
  gate_scores JSONB NOT NULL DEFAULT '{}',
  error TEXT,
  attempts INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  checked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS link_events_pending_idx ON link_events (created_at)
WHERE
  status = 'pending';

CREATE INDEX IF NOT EXISTS link_events_domain_idx ON link_events (domain_id, status);

CREATE TABLE IF NOT EXISTS link_checked (
  post_id UUID PRIMARY KEY REFERENCES posts (id) ON DELETE CASCADE,
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Write the temporary check**

Create `internal/service/ingest/check_test.go`:

```go
package ingest

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

func checkDB(t *testing.T) *database.Database {
	t.Helper()

	conn := os.Getenv("INGEST_CHECK_CONN")
	if conn == "" {
		t.Skip("INGEST_CHECK_CONN not set")
	}

	db, err := database.NewDatabase(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(db.Close)

	return db
}

func TestExtractionCheck(t *testing.T) {
	ctx := context.Background()
	db := checkDB(t)

	if _, err := db.Exec(ctx, `
		INSERT INTO domains (id, base_url) VALUES
			('00000000-0000-0000-0000-000000000001', 'https://own.example'),
			('00000000-0000-0000-0000-000000000004', 'https://other.example');
		INSERT INTO sources (id, domain_id, adapter, url) VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'rss', 'https://own.example/rss');
		INSERT INTO sources (id, domain_id, adapter, url, origin, status) VALUES ('00000000-0000-0000-0000-000000000005', '00000000-0000-0000-0000-000000000004', 'page', 'https://other.example', 'discovered', 'proposed');
		INSERT INTO posts (id, source_id, domain_id, url, lang, title, short_content, content, content_hash, created_at) VALUES
			('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'https://own.example/p1', 'sv', 'p1', '',
				'[a](https://news.example/a) [own](https://www.own.example/x) [p](https://existing.example/p) [q](https://queued.example/q) [fb](https://facebook.com/v)', '\x00', now()),
			('00000000-0000-0000-0000-0000000000a2', '00000000-0000-0000-0000-000000000005', '00000000-0000-0000-0000-000000000004', 'https://other.example/p2', 'sv', 'p2', '',
				'[u](https://untrusted.example/u)', '\x00', now()),
			('00000000-0000-0000-0000-0000000000a3', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'https://own.example/p3', 'sv', 'p3', '',
				'[o](https://old.example/o)', '\x00', now() - interval '8 days'),
			('00000000-0000-0000-0000-0000000000a4', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'https://existing.example/p', 'sv', 'p4', '', 'Inga länkar.', '\x00', now());
		INSERT INTO link_events (url, via, from_post_id) VALUES ('https://queued.example/q', 'link', '00000000-0000-0000-0000-0000000000a4');
		`); err != nil {
		t.Fatal(err)
	}

	s := NewStore(db)

	if err := extractLinks(ctx, &s, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query(ctx, `SELECT url FROM link_events ORDER BY url`)
	if err != nil {
		t.Fatal(err)
	}

	urls, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"https://news.example/a", "https://queued.example/q"}; !slices.Equal(urls, want) {
		t.Errorf("queued %v", urls)
	}

	var via, from string

	if err := db.QueryRow(ctx, `SELECT via, from_post_id::text FROM link_events WHERE url = 'https://news.example/a'`).Scan(&via, &from); err != nil {
		t.Fatal(err)
	}

	if via != "link" || from != "00000000-0000-0000-0000-0000000000a1" {
		t.Errorf("via %s from %s", via, from)
	}

	rows, err = db.Query(ctx, `SELECT post_id::text FROM link_checked ORDER BY post_id`)
	if err != nil {
		t.Fatal(err)
	}

	checked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"00000000-0000-0000-0000-0000000000a1", "00000000-0000-0000-0000-0000000000a4"}; !slices.Equal(checked, want) {
		t.Errorf("checked %v", checked)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go vet ./internal/service/ingest/`
Expected: FAIL with `undefined: NewStore` and `undefined: extractLinks`.

- [ ] **Step 4: Implement the store**

Create `internal/service/ingest/model.go`:

```go
package ingest

type ScanPost struct {
	ID      string
	Content string
	BaseURL string
}
```

Create `internal/service/ingest/store.go`:

```go
package ingest

import (
	"context"
	"fmt"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	db *database.Database
}

func NewStore(db *database.Database) Store {
	return Store{db: db}
}

func (s *Store) ListUnscannedPosts(ctx context.Context, windowDays, limit int) ([]ScanPost, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.content, d.base_url
		FROM posts p
		JOIN sources s ON s.id = p.source_id
		JOIN domains d ON d.id = p.domain_id
		WHERE s.status IN ('active', 'dormant')
			AND p.created_at > now() - make_interval(days => $1)
			AND NOT EXISTS (SELECT 1 FROM link_checked c WHERE c.post_id = p.id)
		ORDER BY p.created_at
		LIMIT $2
		`, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("err query unscanned posts: %w", err)
	}

	posts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (p ScanPost, err error) {
		err = row.Scan(&p.ID, &p.Content, &p.BaseURL)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan unscanned posts: %w", err)
	}

	return posts, nil
}

func (s *Store) QueueLinks(ctx context.Context, postID string, urls []string) (queued int64, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO link_events (url, via, from_post_id)
			SELECT u, 'link', $2 FROM unnest($1::text[]) AS u
			WHERE NOT EXISTS (SELECT 1 FROM posts p WHERE p.url = u)
			ON CONFLICT (url) DO NOTHING
			`, urls, postID)
		if err != nil {
			return fmt.Errorf("err queue links: %w", err)
		}

		queued = tag.RowsAffected()

		if _, err := tx.Exec(ctx, `INSERT INTO link_checked (post_id) VALUES ($1) ON CONFLICT (post_id) DO NOTHING`, postID); err != nil {
			return fmt.Errorf("err mark link checked: %w", err)
		}

		return nil
	})

	return
}
```

- [ ] **Step 5: Implement the worker**

Create `internal/service/ingest/extract.go`:

```go
package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagulv/kharoborathus/internal/lib/schedule"
)

const (
	scanInterval  = time.Minute
	scanBatch     = 50
	pendingWindow = 7
)

func RunExtraction(ctx context.Context, s *Store, l *slog.Logger) error {
	for range schedule.Always.Tick(ctx, scanInterval) {
		if err := extractLinks(ctx, s, l); err != nil {
			l.Error("extract links", "err", err)
		}
	}

	return ctx.Err()
}

func extractLinks(ctx context.Context, s *Store, l *slog.Logger) error {
	posts, err := s.ListUnscannedPosts(ctx, pendingWindow, scanBatch)
	if err != nil {
		return err
	}

	var queued int64

	for _, p := range posts {
		n, err := s.QueueLinks(ctx, p.ID, outboundLinks(p.Content, p.BaseURL))
		if err != nil {
			return err
		}

		queued += n
	}

	if len(posts) > 0 {
		l.Info("link extraction", "processed", len(posts), "queued", queued)
	}

	return nil
}
```

- [ ] **Step 6: Run the check**

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" ingest_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d ingest_check' < internal/migrations/schema.sql
(set -a; . ./.env; INGEST_CHECK_CONN="postgresql://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:5432/ingest_check?sslmode=disable" go test ./internal/service/ingest/ -run Check -count=1)
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" ingest_check'
```

Expected: `ok`. `p2` is untrusted, `p3` is outside the window, `existing.example/p` is a post, `queued.example/q` was queued, the own and Facebook links are filtered.

- [ ] **Step 7: Wire the worker**

In `cmd/main.go` import `"github.com/dagulv/kharoborathus/internal/service/ingest"`, add after `switchStore := switches.NewStore(db)`:

```go
	ingestStore := ingest.NewStore(db)
```

and after the discovery `g.Go` block:

```go
	g.Go(func() error {
		return ingest.RunExtraction(ctx, &ingestStore, l.WithGroup("links"))
	})
```

- [ ] **Step 8: Delete the check, build and test**

```bash
rm internal/service/ingest/check_test.go
go build ./... && go vet ./... && go test ./...
```

Expected: all ok.

- [ ] **Step 9: Commit**

```bash
git add internal/migrations/schema.sql internal/service/ingest/model.go internal/service/ingest/store.go internal/service/ingest/extract.go cmd/main.go
git commit -m "Queue outbound links from trusted posts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Hard gate

**Files:**

- Create: `internal/service/robot/prompts/article.md`, `internal/service/robot/gate.go`
- Modify: `internal/service/robot/tagger.go`, `internal/service/tagging/store.go` (`ListTags`)
- Test: `internal/service/robot/gate_test.go`

**Interfaces:**

- Produces: `robot.Tag.Gate bool`. `func (t *Tagger) Gate(ctx context.Context, title, markdown string, tags []Tag) (Verdict, error)` with `type Verdict struct { Article float64; Tags map[string]float64; Keys []string; Passed bool }`, JSON `{"article": ..., "tags": {...}}`. `tagging.Store.ListTags` fills `Gate`. Task 8 calls both.

- [ ] **Step 1: Write the failing test**

Create `internal/service/robot/gate_test.go`:

```go
package robot

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

var gateTags = []Tag{
	{Key: "acquisition", Terms: []string{"förvärv"}, Gate: true},
	{Key: "sweden"},
}

const (
	acquisitionQuestion = "Is this news article about acquisition (förvärv)?"
	swedenQuestion      = "Is this news article about sweden?"
)

func TestGatePasses(t *testing.T) {
	c := &fakeClassifier{yes: map[string]bool{
		strings.TrimSpace(articlePrompt): true,
		acquisitionQuestion:              true,
		swedenQuestion:                   true,
	}}

	v, err := NewTagger(c).Gate(context.Background(), "Volvo köper", "Volvo köper Northvolt.", gateTags)
	if err != nil {
		t.Fatal(err)
	}

	if !v.Passed || v.Article != 0.95 || !slices.Equal(v.Keys, []string{"acquisition", "sweden"}) {
		t.Errorf("verdict %+v", v)
	}

	if len(c.requests) != 1 || len(c.requests[0]) != 3 {
		t.Fatalf("requests %+v", c.requests)
	}

	if q := c.requests[0][articleID]; q.Criteria == nil || q.Criteria.True == "" || q.Criteria.False == "" {
		t.Errorf("article question %+v", q)
	}
}

func TestGateNeedsGateTag(t *testing.T) {
	c := &fakeClassifier{yes: map[string]bool{
		strings.TrimSpace(articlePrompt): true,
		swedenQuestion:                   true,
	}}

	v, err := NewTagger(c).Gate(context.Background(), "Sverige", "Om Sverige.", gateTags)
	if err != nil {
		t.Fatal(err)
	}

	if v.Passed || !slices.Equal(v.Keys, []string{"sweden"}) {
		t.Errorf("verdict %+v", v)
	}
}

func TestGateNeedsArticle(t *testing.T) {
	c := &fakeClassifier{yes: map[string]bool{acquisitionQuestion: true}}

	v, err := NewTagger(c).Gate(context.Background(), "Förvärv", "Lista över förvärv.", gateTags)
	if err != nil {
		t.Fatal(err)
	}

	if v.Passed || v.Article != 0.1 {
		t.Errorf("verdict %+v", v)
	}
}

func TestGateBatches(t *testing.T) {
	tags := slices.Clone(gateTags)

	for i := range 60 {
		tags = append(tags, Tag{Key: fmt.Sprintf("filler-%d", i)})
	}

	c := &fakeClassifier{}

	if _, err := NewTagger(c).Gate(context.Background(), "t", "a", tags); err != nil {
		t.Fatal(err)
	}

	if len(c.requests) != 2 || len(c.requests[0]) != tagsPerRequest+1 || len(c.requests[1]) != 12 {
		t.Fatalf("requests of %d and %d questions", len(c.requests[0]), len(c.requests[1]))
	}

	if _, ok := c.requests[1][articleID]; ok {
		t.Error("article question asked twice")
	}
}

func TestVerdictJSON(t *testing.T) {
	b, err := json.Marshal(Verdict{Article: 0.9, Tags: map[string]float64{"k": 0.5}, Keys: []string{"k"}, Passed: true})
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != `{"article":0.9,"tags":{"k":0.5}}` {
		t.Errorf("json %s", b)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/service/robot/`
Expected: FAIL with `unknown field Gate in struct literal` and `undefined: articlePrompt`.

- [ ] **Step 3: Prompt**

Create `internal/service/robot/prompts/article.md`:

```markdown
Does this page report a specific business event, like a news article, press release or filing?
```

- [ ] **Step 4: Share the classification between Tags and Gate**

In `internal/service/robot/tagger.go` add `Gate bool` to `Tag`:

```go
type Tag struct {
	Key   string
	Terms []string
	Gate  bool
}
```

Replace `Tags` with:

```go
func (t *Tagger) Tags(ctx context.Context, title, markdown string, tags []Tag) ([]string, error) {
	scores, _, err := t.classify(ctx, title, markdown, tags, nil)
	if err != nil {
		return nil, err
	}

	return matched(tags, scores), nil
}

// Extra questions ride in the first batch, so they cost no extra request.
func (t *Tagger) classify(ctx context.Context, title, markdown string, tags []Tag, extra map[string]adapter.NoulQuestion) (scores, extraScores map[string]float64, err error) {
	if r := []rune(markdown); len(r) > maxTagState {
		markdown = string(r[:maxTagState])
	}

	state := map[string]string{"title": title, "article": markdown}
	scores = make(map[string]float64, len(tags))

	for start := 0; start == 0 || start < len(tags); start += tagsPerRequest {
		batch := tags[start:min(start+tagsPerRequest, len(tags))]
		questions := make(map[string]adapter.NoulQuestion, len(batch)+len(extra))

		for i, tag := range batch {
			var b strings.Builder

			if err := tagQuestion.Execute(&b, tag); err != nil {
				return nil, nil, err
			}

			questions[fmt.Sprintf("tag_%d", i)] = adapter.NoulQuestion{Instructions: b.String()}
		}

		if start == 0 {
			maps.Copy(questions, extra)
		}

		if len(questions) == 0 {
			break
		}

		answers, err := t.classifier.Noul(ctx, state, questions)
		if err != nil {
			return nil, nil, err
		}

		for i, tag := range batch {
			scores[tag.Key] = answers[fmt.Sprintf("tag_%d", i)]
		}

		if start == 0 {
			extraScores = answers
		}
	}

	return scores, extraScores, nil
}

func matched(tags []Tag, scores map[string]float64) []string {
	var keys []string

	for _, tag := range tags {
		if scores[tag.Key] >= tagThreshold {
			keys = append(keys, tag.Key)
		}
	}

	return keys
}
```

Add `"maps"` to the imports.

- [ ] **Step 5: Gate**

Create `internal/service/robot/gate.go`:

```go
package robot

import (
	"context"
	_ "embed"
	"slices"
	"strings"

	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
)

const articleID = "article"

//go:embed prompts/article.md
var articlePrompt string

var articleCriteria = adapter.NoulCriteria{
	True:  "It reports one concrete event at a named company or organisation, like a deal, a result, an appointment, an order or a filing.",
	False: "It is a listing, category, product, profile, guide, opinion or login page, or it mentions events only in passing.",
}

type Verdict struct {
	Article float64            `json:"article"`
	Tags    map[string]float64 `json:"tags"`
	Keys    []string           `json:"-"`
	Passed  bool               `json:"-"`
}

func (t *Tagger) Gate(ctx context.Context, title, markdown string, tags []Tag) (Verdict, error) {
	scores, extra, err := t.classify(ctx, title, markdown, tags, map[string]adapter.NoulQuestion{
		articleID: {Instructions: strings.TrimSpace(articlePrompt), Criteria: &articleCriteria},
	})
	if err != nil {
		return Verdict{}, err
	}

	v := Verdict{Article: extra[articleID], Tags: scores, Keys: matched(tags, scores)}
	v.Passed = v.Article >= tagThreshold && slices.ContainsFunc(tags, func(tag Tag) bool {
		return tag.Gate && scores[tag.Key] >= tagThreshold
	})

	return v, nil
}
```

- [ ] **Step 6: Run the robot tests**

Run: `go test ./internal/service/robot/`
Expected: `ok`, including the unchanged `TestTags`.

- [ ] **Step 7: ListTags fills Gate**

In `internal/service/tagging/store.go` `ListTags`, add `t.gate` as the third select column:

```sql
		SELECT
			t.key,
			COALESCE(array_agg(tt.term ORDER BY tt.lang, tt.term) FILTER (WHERE tt.term IS NOT NULL), '{}'),
			t.gate
```

and scan it: `err = row.Scan(&tag.Key, &tag.Terms, &tag.Gate)`.

- [ ] **Step 8: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all ok.

- [ ] **Step 9: Commit**

```bash
git add internal/service/robot/prompts/article.md internal/service/robot/gate.go internal/service/robot/gate_test.go internal/service/robot/tagger.go internal/service/tagging/store.go
git commit -m "Add hard gate with article question to the tagging call

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: robots.txt and per-domain pacing

**Files:**

- Create: `internal/service/ingest/robots.go`, `internal/service/ingest/pacer.go`
- Test: `internal/service/ingest/robots_test.go`, `internal/service/ingest/pacer_test.go`

**Interfaces:**

- Consumes: `hostOf` from Task 3.
- Produces: `newPacer(gap time.Duration) *pacer` with field `gap` and `Wait(ctx context.Context, host string) error`. `NewRobots(client *http.Client, p *pacer) *Robots` with `Allowed(ctx context.Context, rawURL string) (bool, error)`. Const `userAgent = "kharoborathus/1.0"`. Task 8 uses all of them.

- [ ] **Step 1: Write the failing tests**

Create `internal/service/ingest/robots_test.go`:

```go
package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRobots(t *testing.T) {
	cases := []struct {
		name, body, path string
		want             bool
	}{
		{"wildcard disallow", "User-agent: *\nDisallow: /private", "/private/a", false},
		{"wildcard other path", "User-agent: *\nDisallow: /private", "/news/a", true},
		{"specific group wins", "User-agent: *\nDisallow: /\n\nUser-agent: Kharoborathus\nDisallow: /x", "/a", true},
		{"specific group rule", "User-agent: *\nDisallow: /\n\nUser-agent: kharoborathus\nDisallow: /x", "/x/y", false},
		{"specific empty disallow", "User-agent: *\nDisallow: /\n\nUser-agent: kharoborathus\nDisallow:", "/a", true},
		{"longest match allow", "User-agent: *\nDisallow: /a\nAllow: /a/b", "/a/b/c", true},
		{"longest match disallow", "User-agent: *\nDisallow: /a\nAllow: /a/b", "/a/c", false},
		{"tie allows", "User-agent: *\nDisallow: /a\nAllow: /a", "/a", true},
		{"star and end", "User-agent: *\nDisallow: /*.pdf$", "/f.pdf", false},
		{"end anchors", "User-agent: *\nDisallow: /*.pdf$", "/f.pdf?x=1", true},
		{"comments and case", "USER-AGENT: * # all\nDISALLOW: /x # no", "/x", false},
		{"grouped agents", "User-agent: other\nUser-agent: *\nDisallow: /x", "/x", false},
		{"other agent only", "User-agent: other\nDisallow: /", "/x", true},
		{"empty", "", "/x", true},
	}

	for _, c := range cases {
		if got := parseRobots([]byte(c.body), robotsAgent).allowed(c.path); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func robotsServer(t *testing.T, status int, body string) (*httptest.Server, *int) {
	t.Helper()

	var requests int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/robots.txt" {
			t.Errorf("request to %s", r.URL.Path)
		}

		if r.UserAgent() != userAgent {
			t.Errorf("user agent %q", r.UserAgent())
		}

		requests++
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))

	t.Cleanup(srv.Close)

	return srv, &requests
}

func TestRobotsAllowed(t *testing.T) {
	srv, requests := robotsServer(t, http.StatusOK, "User-agent: *\nDisallow: /private")
	r := NewRobots(srv.Client(), newPacer(0))

	if ok, err := r.Allowed(context.Background(), srv.URL+"/private/1"); err != nil || ok {
		t.Errorf("private: %v %v", ok, err)
	}

	if ok, err := r.Allowed(context.Background(), srv.URL+"/news/1"); err != nil || !ok {
		t.Errorf("news: %v %v", ok, err)
	}

	if *requests != 1 {
		t.Errorf("%d robots requests", *requests)
	}
}

func TestRobotsMissing(t *testing.T) {
	srv, _ := robotsServer(t, http.StatusNotFound, "")

	if ok, err := NewRobots(srv.Client(), newPacer(0)).Allowed(context.Background(), srv.URL+"/x"); err != nil || !ok {
		t.Errorf("got %v %v", ok, err)
	}
}

func TestRobotsServerError(t *testing.T) {
	srv, requests := robotsServer(t, http.StatusServiceUnavailable, "")
	r := NewRobots(srv.Client(), newPacer(0))

	for range 2 {
		if _, err := r.Allowed(context.Background(), srv.URL+"/x"); err == nil {
			t.Error("no error")
		}
	}

	if *requests != 2 {
		t.Errorf("%d robots requests, errors must not be cached", *requests)
	}
}
```

Create `internal/service/ingest/pacer_test.go`:

```go
package ingest

import (
	"context"
	"testing"
	"time"
)

func TestPacer(t *testing.T) {
	gap := 100 * time.Millisecond
	p := newPacer(gap)
	ctx := context.Background()

	start := time.Now()

	for _, host := range []string{"a.example", "b.example"} {
		if err := p.Wait(ctx, host); err != nil {
			t.Fatal(err)
		}
	}

	if elapsed := time.Since(start); elapsed >= gap {
		t.Errorf("first visits waited %v", elapsed)
	}

	if err := p.Wait(ctx, "a.example"); err != nil {
		t.Fatal(err)
	}

	if elapsed := time.Since(start); elapsed < gap {
		t.Errorf("second visit after %v", elapsed)
	}
}

func TestPacerCancel(t *testing.T) {
	p := newPacer(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())

	if err := p.Wait(ctx, "a.example"); err != nil {
		t.Fatal(err)
	}

	cancel()

	if err := p.Wait(ctx, "a.example"); err == nil {
		t.Error("no error after cancel")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/service/ingest/`
Expected: FAIL with `undefined: parseRobots` and `undefined: newPacer`.

- [ ] **Step 3: Pacer**

Create `internal/service/ingest/pacer.go`:

```go
package ingest

import (
	"context"
	"time"
)

type pacer struct {
	gap  time.Duration
	last map[string]time.Time
}

func newPacer(gap time.Duration) *pacer {
	return &pacer{gap: gap, last: make(map[string]time.Time)}
}

func (p *pacer) Wait(ctx context.Context, host string) error {
	if wait := time.Until(p.last[host].Add(p.gap)); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	p.last[host] = time.Now()

	return nil
}
```

- [ ] **Step 4: Robots**

Create `internal/service/ingest/robots.go`:

```go
package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	userAgent     = "kharoborathus/1.0"
	robotsAgent   = "kharoborathus"
	robotsTTL     = 24 * time.Hour
	maxRobotsSize = 512 << 10
)

type robotsRule struct {
	allow   bool
	pattern *regexp.Regexp
	length  int
}

type robotsRules []robotsRule

type robotsEntry struct {
	rules     robotsRules
	fetchedAt time.Time
}

type Robots struct {
	client *http.Client
	pacer  *pacer
	cache  map[string]robotsEntry
}

func NewRobots(client *http.Client, p *pacer) *Robots {
	return &Robots{client: client, pacer: p, cache: make(map[string]robotsEntry)}
}

func (r *Robots) Allowed(ctx context.Context, rawURL string) (bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Errorf("err parse url: %w", err)
	}

	rules, err := r.rules(ctx, u)
	if err != nil {
		return false, err
	}

	return rules.allowed(u.RequestURI()), nil
}

func (r *Robots) rules(ctx context.Context, u *url.URL) (robotsRules, error) {
	origin := strings.ToLower(u.Scheme + "://" + u.Host)

	if e, ok := r.cache[origin]; ok && time.Since(e.fetchedAt) < robotsTTL {
		return e.rules, nil
	}

	if err := r.pacer.Wait(ctx, hostOf(origin)); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/robots.txt", nil)
	if err != nil {
		return nil, fmt.Errorf("err new robots request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

	res, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("err fetch robots.txt: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= http.StatusInternalServerError || res.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("err fetch robots.txt: status %s", res.Status)
	}

	// Any other non-200 status means there is no robots.txt, which allows everything.
	var rules robotsRules

	if res.StatusCode == http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(res.Body, maxRobotsSize))
		if err != nil {
			return nil, fmt.Errorf("err read robots.txt: %w", err)
		}

		rules = parseRobots(body, robotsAgent)
	}

	r.cache[origin] = robotsEntry{rules: rules, fetchedAt: time.Now()}

	return rules, nil
}

func parseRobots(body []byte, agent string) robotsRules {
	var specific, wildcard robotsRules
	var agents []string
	var hasSpecific, inRules bool

	for line := range strings.Lines(string(body)) {
		line, _, _ = strings.Cut(line, "#")

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)

		switch key {
		case "user-agent":
			if inRules {
				agents, inRules = nil, false
			}

			value = strings.ToLower(value)
			agents = append(agents, value)
			hasSpecific = hasSpecific || value == agent
		case "allow", "disallow":
			inRules = true

			if value == "" {
				continue
			}

			rule := robotsRule{allow: key == "allow", pattern: robotsPattern(value), length: len(value)}

			for _, a := range agents {
				switch a {
				case agent:
					specific = append(specific, rule)
				case "*":
					wildcard = append(wildcard, rule)
				}
			}
		}
	}

	if hasSpecific {
		return specific
	}

	return wildcard
}

func robotsPattern(path string) *regexp.Regexp {
	expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(strings.TrimSuffix(path, "$")), `\*`, ".*")

	if strings.HasSuffix(path, "$") {
		expr += "$"
	}

	return regexp.MustCompile(expr)
}

// The longest matching rule wins, and allow wins a tie.
func (r robotsRules) allowed(path string) bool {
	best, allow := -1, true

	for _, rule := range r {
		if rule.length < best || !rule.pattern.MatchString(path) {
			continue
		}

		if rule.length > best || rule.allow {
			best, allow = rule.length, rule.allow
		}
	}

	return allow
}
```

- [ ] **Step 5: Run them to verify they pass**

Run: `gofmt -l internal/service/ingest; go vet ./internal/service/ingest/ && go test ./internal/service/ingest/`
Expected: no files listed, `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/service/ingest/robots.go internal/service/ingest/robots_test.go internal/service/ingest/pacer.go internal/service/ingest/pacer_test.go
git commit -m "Add robots.txt check and per-domain pacing for ingest

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Reusable post insert, duplicate check and markdown

**Files:**

- Modify: `internal/service/feed/store.go`
- Modify: `internal/service/source/sourced.go`
- Test: `internal/service/source/markdown_test.go`

**Interfaces:**

- Produces: `func feed.InsertFeed(ctx context.Context, tx pgx.Tx, f Feed) (Feed, error)`, returning an error matching `pgx.ErrNoRows` on a URL conflict. `func (s *feed.Store) HasDuplicate(ctx context.Context, content string) (bool, error)`. `func source.Markdown(f feed.Feed) (feed.Feed, error)`. Task 8 calls all three.

- [ ] **Step 1: Split CreateFeed**

In `internal/service/feed/store.go` replace `CreateFeed` with:

```go
func (s *Store) CreateFeed(ctx context.Context, feed Feed) (out Feed, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		out, err = InsertFeed(ctx, tx, feed)
		return err
	})

	return
}

func InsertFeed(ctx context.Context, tx pgx.Tx, feed Feed) (out Feed, err error) {
	if feed.PostedAt.IsZero() {
		feed.PostedAt = time.Now()
	}

	hash := sha256.Sum256([]byte(feed.Content))

	out, err = scanFeed(tx.QueryRow(ctx, `
		INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, tags, authors, posted_at, content_hash, simhash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], '{}'), COALESCE($9::text[], '{}'), $10, $11, $12)
		ON CONFLICT (url) DO NOTHING
		RETURNING `+feedColumns,
		feed.SourceID, feed.DomainID, feed.URL, feed.Lang, feed.Title, feed.ShortContent, feed.Content, feed.Tags, feed.Authors, feed.PostedAt, hash[:], textSimhash(feed.Content),
	))
	if err != nil {
		return out, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO post_links (from_post_id, to_post_id, relation, metadata)
		SELECT n.id, o.id, 'duplicate_of', jsonb_build_object('distance', o.distance)
		FROM posts n
		CROSS JOIN LATERAL (
			SELECT p.id, bit_count((p.simhash # n.simhash)::bit(64)) AS distance
			FROM posts p
			WHERE p.created_at < n.created_at
				AND p.created_at > n.created_at - $2 * interval '1 day'
				AND bit_count((p.simhash # n.simhash)::bit(64)) <= $3
			ORDER BY p.created_at
			LIMIT 1
		) o
		WHERE n.id = $1
		`, out.ID, duplicateWindowDays, maxDuplicateDistance); err != nil {
		return out, fmt.Errorf("err link duplicate: %w", err)
	}

	return out, nil
}
```

The insert scan error stays unwrapped, as before, since `sourced.go` matches `sql.ErrNoRows` on it.

- [ ] **Step 2: Duplicate check**

Add after `InsertFeed`:

```go
func (s *Store) HasDuplicate(ctx context.Context, content string) (bool, error) {
	hash := textSimhash(content)
	if hash == nil {
		return false, nil
	}

	var exists bool

	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM posts
			WHERE created_at > now() - $2 * interval '1 day'
				AND bit_count((simhash # $1::bigint)::bit(64)) <= $3
		)
		`, *hash, duplicateWindowDays, maxDuplicateDistance).Scan(&exists); err != nil {
		return false, fmt.Errorf("err check duplicate: %w", err)
	}

	return exists, nil
}
```

`HasDuplicate` is verified end to end in Task 8 (`/news/volvo-copy`).

- [ ] **Step 3: Build and test the feed change**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all ok.

- [ ] **Step 4: Commit the feed change**

```bash
git add internal/service/feed/store.go
git commit -m "Expose post insert in a caller transaction and duplicate check

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 5: Write the failing markdown test**

Create `internal/service/source/markdown_test.go`:

```go
package source

import (
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/service/feed"
)

func TestMarkdown(t *testing.T) {
	f, err := Markdown(feed.Feed{URL: "https://a.example/news/1", Content: `<p>Volvo <a href="/x">köper</a> Northvolt.</p>`})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.Content, "[köper](https://a.example/x)") {
		t.Errorf("content %q", f.Content)
	}

	if f.ShortContent != "Volvo köper Northvolt." {
		t.Errorf("short content %q", f.ShortContent)
	}
}

func TestMarkdownKeepsShortContent(t *testing.T) {
	f, err := Markdown(feed.Feed{URL: "https://a.example/news/1", ShortContent: "Från flödet.", Content: "<p>Text.</p>"})
	if err != nil {
		t.Fatal(err)
	}

	if f.ShortContent != "Från flödet." {
		t.Errorf("short content %q", f.ShortContent)
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./internal/service/source/ -run Markdown`
Expected: FAIL with `undefined: Markdown`.

- [ ] **Step 7: Extract Markdown**

In `internal/service/source/sourced.go` add:

```go
func Markdown(f feed.Feed) (feed.Feed, error) {
	if f.ShortContent == "" {
		f.ShortContent = excerpt(f.Content)
	}

	md, err := htmltomarkdown.ConvertString(f.Content, converter.WithDomain(f.URL))
	if err != nil {
		return f, fmt.Errorf("err convert to markdown: %w", err)
	}

	f.Content = md

	return f, nil
}
```

Add `"fmt"` to the imports. Replace the loop body of `markdownFeed` up to the `select` with:

```go
		for f := range in {
			md, err := Markdown(f)
			if err != nil {
				l.Error("convert to markdown", "source", f.SourceID, "url", f.URL, "err", err)
			}

			select {
			case out <- md:
			case <-ctx.Done():
				return
			}
		}
```

On error `Markdown` returns the feed with its excerpt set and the HTML content kept, as the stage did before.

- [ ] **Step 8: Run tests**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all ok.

- [ ] **Step 9: Commit**

```bash
git add internal/service/source/sourced.go internal/service/source/markdown_test.go
git commit -m "Export markdown conversion from the source pipeline

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Discovered ingest worker

**Files:**

- Modify: `internal/migrations/schema.sql` (switch row)
- Modify: `internal/service/switches/store.go`
- Modify: `internal/service/ingest/model.go`, `internal/service/ingest/store.go`
- Create: `internal/service/ingest/ingest.go`
- Modify: `cmd/main.go`
- Temporary: `internal/service/ingest/check_test.go` (deleted before commit)

**Interfaces:**

- Consumes: `link_events` and `Store` from Task 4, `hostOf` from Task 3, `Tagger.Gate`, `Verdict`, `Tag.Gate` and `tagging.Store.ListTags` from Task 5, `Robots`, `pacer`, `userAgent` from Task 6, `feed.InsertFeed`, `feed.Store.HasDuplicate`, `source.Markdown` from Task 7, `source.Trim.Apply`, `adapter.FetchHTML`, `domain.Store.GetOrCreateDomain`, `robot.Summarizer.Summarize`.
- Produces: `switches.DiscoveredIngest = "discovered_ingest"`. `ingest.NewIngester(...) *Ingester` and `(*Ingester).Run(ctx) error`. `link_events` rows leave `pending`.

- [ ] **Step 1: Switch row and key**

In `internal/migrations/schema.sql` replace the last line with:

```sql
INSERT INTO switches (key) VALUES ('shadow_gate'), ('discovered_ingest') ON CONFLICT (key) DO NOTHING;
```

In `internal/service/switches/store.go` replace the const with:

```go
const (
	ShadowGate       = "shadow_gate"
	DiscoveredIngest = "discovered_ingest"
)
```

- [ ] **Step 2: Write the temporary check**

Create `internal/service/ingest/check_test.go`:

```go
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/database"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source"
	"github.com/dagulv/kharoborathus/internal/service/switches"
	"github.com/dagulv/kharoborathus/internal/service/tagging"
)

const (
	volvoText    = `<p>Volvo Cars tecknar avtal om att förvärva det svenska batteribolaget Northvolts fabrik i Göteborg.</p><p>Affären omfattar anläggningen i Torslanda samt omkring 300 anställda som går över till Volvo Cars.</p><p>Köpeskillingen uppgår till 1,2 miljarder kronor och betalas kontant vid tillträdet som väntas ske under det fjärde kvartalet.</p><p>Aktien steg med tre procent i tidig handel på Stockholmsbörsen efter beskedet.</p>`
	ericssonText = `<p>Ericsson redovisar ett rörelseresultat för tredje kvartalet som överträffar analytikernas förväntningar.</p><p>Nettoomsättningen minskade med fyra procent jämfört med samma period i fjol, främst på grund av svag efterfrågan i Nordamerika.</p><p>Bruttomarginalen förbättrades dock till 47 procent tack vare kostnadsbesparingar.</p><p>Bolaget upprepar sin prognos för helåret.</p>`
	weatherText  = `<p>Det blir mestadels soligt i hela landet under helgen med temperaturer kring tjugo grader i söder.</p><p>I norra Norrland väntas enstaka regnskurar under söndagen och det kan blåsa upp längs kusten.</p><p>Nästa vecka ser ut att bli något kyligare med ostadigt väder i Svealand och Götaland.</p>`
)

func checkDB(t *testing.T) *database.Database {
	t.Helper()

	conn := os.Getenv("INGEST_CHECK_CONN")
	if conn == "" {
		t.Skip("INGEST_CHECK_CONN not set")
	}

	db, err := database.NewDatabase(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(db.Close)

	return db
}

func page(title, body string) string {
	return fmt.Sprintf(`<!doctype html><html lang="sv"><head><title>%s</title></head><body><nav><a href="/">Hem</a></nav><article><h1>%s</h1>%s</article><footer>Kontakt</footer></body></html>`, title, title, body)
}

type checkLLM struct{}

func (checkLLM) Schedule() schedule.Schedule { return schedule.Always }

func (checkLLM) Prompt(context.Context, adapter.Request) (string, error) {
	return "", adapter.ErrUnavailable
}

func (checkLLM) Unmarshal(context.Context, adapter.Request, any) error {
	return adapter.ErrUnavailable
}

type checkClassifier struct{}

func (checkClassifier) Noul(_ context.Context, state any, questions map[string]adapter.NoulQuestion) (map[string]float64, error) {
	text := fmt.Sprint(state)
	out := make(map[string]float64, len(questions))

	for id := range questions {
		out[id] = 0.05

		if id == "article" || strings.Contains(text, "procent") {
			out[id] = 0.95
		}
	}

	return out, nil
}

func TestIngestCheck(t *testing.T) {
	ctx := context.Background()
	db := checkDB(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /private\n")
		case "/news/volvo", "/news/volvo-copy", "/news/old":
			fmt.Fprint(w, page("Volvo Cars köper Northvolts fabrik", volvoText))
		case "/news/ericsson":
			fmt.Fprint(w, page("Ericsson slår förväntningarna", ericssonText))
		case "/news/weather":
			fmt.Fprint(w, page("Soligt i helgen", weatherText))
		case "/news/short":
			fmt.Fprint(w, page("Kort", "<p>Kort.</p>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if _, err := db.Exec(ctx, `
		INSERT INTO domains (id, base_url) VALUES ('00000000-0000-0000-0000-000000000001', 'https://own.example');
		INSERT INTO sources (id, domain_id, adapter, url, enabled) VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'rss', 'https://own.example/rss', true);
		INSERT INTO posts (id, source_id, domain_id, url, lang, title, short_content, content, content_hash) VALUES
			('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'https://own.example/p1', 'sv', 'p1', '', '', '\x00');
		INSERT INTO tags (key, gate) VALUES ('resultat', true);
		`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, content_hash)
		VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', $1, 'sv', 'e', '', '', '\x00')
		`, srv.URL+"/news/ericsson"); err != nil {
		t.Fatal(err)
	}

	queue := []struct {
		path     string
		age      string
		attempts int
	}{
		{"/news/volvo", "6 minutes", 0},
		{"/news/volvo-copy", "5 minutes", 0},
		{"/news/ericsson", "4 minutes", 0},
		{"/news/weather", "3 minutes", 0},
		{"/news/short", "2 minutes", 0},
		{"/private/x", "1 minute", 0},
		{"/missing", "0", 2},
		{"/news/old", "9 days", 0},
	}

	for _, q := range queue {
		if _, err := db.Exec(ctx, `
			INSERT INTO link_events (url, via, from_post_id, created_at, attempts, checked_at)
			VALUES ($1, 'link', '00000000-0000-0000-0000-0000000000a1', now() - $2::interval, $3, CASE WHEN $3 > 0 THEN now() - interval '1 hour' END)
			`, srv.URL+q.path, q.age, q.attempts); err != nil {
			t.Fatal(err)
		}
	}

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := NewStore(db)
	sw := switches.NewStore(db)
	tags := tagging.NewStore(db)
	feeds := feed.NewStore(db)
	domains := domain.NewStore(db)
	llm := checkLLM{}

	in := NewIngester(&s, &sw, &tags, &feeds, &domains, source.NewTrim(&domains, llm, l), robot.NewTagger(checkClassifier{}), robot.NewSummarizer(llm), l)
	in.pacer.gap = 0

	if err := in.tick(ctx); err != nil {
		t.Fatal(err)
	}

	var touched int

	if err := db.QueryRow(ctx, `SELECT count(*) FROM link_events WHERE status <> 'pending' OR attempts <> CASE WHEN url LIKE '%/missing' THEN 2 ELSE 0 END`).Scan(&touched); err != nil {
		t.Fatal(err)
	}

	if touched != 0 {
		t.Fatalf("%d links touched while the switch was off", touched)
	}

	if _, err := db.Exec(ctx, `UPDATE switches SET enabled = true WHERE key = 'discovered_ingest'`); err != nil {
		t.Fatal(err)
	}

	for range 9 {
		if err := in.tick(ctx); err != nil {
			t.Fatal(err)
		}
	}

	want := map[string]struct {
		status   string
		attempts int
		reason   string
	}{
		"/news/volvo":      {"accepted", 1, ""},
		"/news/volvo-copy": {"duplicate", 1, ""},
		"/news/ericsson":   {"duplicate", 1, "url exists"},
		"/news/weather":    {"rejected", 1, ""},
		"/news/short":      {"rejected", 1, "no content"},
		"/private/x":       {"failed", 1, "disallowed by robots.txt"},
		"/missing":         {"failed", 3, "404"},
		"/news/old":        {"pending", 0, ""},
	}

	rows, err := db.Query(ctx, `SELECT url, status, attempts, coalesce(error, '') FROM link_events`)
	if err != nil {
		t.Fatal(err)
	}

	for rows.Next() {
		var url, status, reason string
		var attempts int

		if err := rows.Scan(&url, &status, &attempts, &reason); err != nil {
			t.Fatal(err)
		}

		path := strings.TrimPrefix(url, srv.URL)
		w := want[path]

		if status != w.status || attempts != w.attempts || !strings.Contains(reason, w.reason) || (w.reason == "" && reason != "") {
			t.Errorf("%s: %s %d %q", path, status, attempts, reason)
		}
	}

	var origin, status, adapterName, lang string
	var enabled, baseURL bool
	var postTags []string

	if err := db.QueryRow(ctx, `
		SELECT s.origin, s.status, s.enabled, s.adapter, s.url = d.base_url, p.tags, p.lang
		FROM posts p JOIN sources s ON s.id = p.source_id JOIN domains d ON d.id = p.domain_id
		WHERE p.url = $1
		`, srv.URL+"/news/volvo").Scan(&origin, &status, &enabled, &adapterName, &baseURL, &postTags, &lang); err != nil {
		t.Fatal(err)
	}

	if origin != "discovered" || status != "proposed" || !enabled || adapterName != "page" || !baseURL || len(postTags) != 1 || postTags[0] != "resultat" || lang != "sv" {
		t.Errorf("post %s %s %v %s %v %v %s", origin, status, enabled, adapterName, baseURL, postTags, lang)
	}

	counts := map[string]string{
		"proposed sources":   `SELECT count(*) FROM sources WHERE status = 'proposed'`,
		"discovered by post": `SELECT count(*) FROM source_discovered_by_post WHERE post_id = '00000000-0000-0000-0000-0000000000a1'`,
		"discovered link":    `SELECT count(*) FROM post_links l JOIN posts p ON p.id = l.from_post_id WHERE p.url LIKE '%/news/volvo' AND l.to_post_id = '00000000-0000-0000-0000-0000000000a1' AND l.relation = 'discovered' AND l.metadata->>'via' = 'link'`,
		"tag checked":        `SELECT count(*) FROM tag_checked c JOIN posts p ON p.id = c.post_id WHERE p.url LIKE '%/news/volvo' AND c.gate_passed IS NULL`,
		"accepted scores":    `SELECT count(*) FROM link_events WHERE url LIKE '%/news/volvo' AND domain_id IS NOT NULL AND gate_scores ? 'article'`,
		"rejected scores":    `SELECT count(*) FROM link_events WHERE url LIKE '%/news/weather' AND gate_scores ? 'article'`,
		"untouched tag":      `SELECT count(*) FROM tags WHERE key = 'resultat' AND last_matched_at IS NULL`,
	}

	for name, q := range counts {
		var n int

		if err := db.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}

		if n != 1 {
			t.Errorf("%s: %d", name, n)
		}
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go vet ./internal/service/ingest/`
Expected: FAIL with `undefined: NewIngester`.

- [ ] **Step 4: Model and store methods**

Add to `internal/service/ingest/model.go`:

```go
const (
	statusAccepted  = "accepted"
	statusRejected  = "rejected"
	statusDuplicate = "duplicate"
	statusFailed    = "failed"
)

type Link struct {
	ID         string
	URL        string
	Via        string
	FromPostID string
}
```

Add to `internal/service/ingest/store.go`, with imports `"errors"` and `"github.com/dagulv/kharoborathus/internal/service/feed"`:

```go
func (s *Store) CheckedLastDay(ctx context.Context) (n int, err error) {
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM link_events WHERE checked_at > now() - interval '1 day'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("err count checked links: %w", err)
	}

	return n, nil
}

// One link per host, so a tick never hits a domain twice. Retries come after fresh links.
func (s *Store) ListPending(ctx context.Context, windowDays, limit int) ([]Link, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, url, via, from_post_id
		FROM (
			SELECT DISTINCT ON (host) id, url, via, from_post_id, checked_at, created_at
			FROM (
				SELECT *, lower(substring(url FROM '^[^:]+://(?:www\.)?([^/:?#]+)')) AS host
				FROM link_events
				WHERE status = 'pending' AND created_at > now() - make_interval(days => $1)
			) q
			ORDER BY host, checked_at NULLS FIRST, created_at
		) h
		ORDER BY checked_at NULLS FIRST, created_at
		LIMIT $2
		`, windowDays, limit)
	if err != nil {
		return nil, fmt.Errorf("err query pending links: %w", err)
	}

	links, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (l Link, err error) {
		err = row.Scan(&l.ID, &l.URL, &l.Via, &l.FromPostID)
		return
	})
	if err != nil {
		return nil, fmt.Errorf("err scan pending links: %w", err)
	}

	return links, nil
}

func (s *Store) Finish(ctx context.Context, id, status, domainID string, scores any, reason string) error {
	if _, err := s.db.Exec(ctx, `
		UPDATE link_events
		SET
			status = $2,
			domain_id = coalesce(NULLIF($3, '')::uuid, domain_id),
			gate_scores = coalesce($4::jsonb, gate_scores),
			error = NULLIF($5, ''),
			attempts = attempts + 1,
			checked_at = now()
		WHERE id = $1
		`, id, status, domainID, scores, reason); err != nil {
		return fmt.Errorf("err finish link: %w", err)
	}

	return nil
}

func (s *Store) Retry(ctx context.Context, id, domainID, reason string, maxAttempts int) (failed bool, err error) {
	if err = s.db.QueryRow(ctx, `
		UPDATE link_events
		SET
			status = CASE WHEN attempts + 1 >= $4 THEN 'failed' ELSE status END,
			domain_id = coalesce(NULLIF($2, '')::uuid, domain_id),
			error = $3,
			attempts = attempts + 1,
			checked_at = now()
		WHERE id = $1
		RETURNING status = 'failed'
		`, id, domainID, reason, maxAttempts).Scan(&failed); err != nil {
		return false, fmt.Errorf("err retry link: %w", err)
	}

	return failed, nil
}

func (s *Store) Accept(ctx context.Context, link Link, f feed.Feed, scores any) (post feed.Feed, err error) {
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id FROM sources WHERE domain_id = $1 ORDER BY created_at LIMIT 1`, f.DomainID).Scan(&f.SourceID)
		if errors.Is(err, pgx.ErrNoRows) {
			f.SourceID, err = proposeSource(ctx, tx, f.DomainID, link.FromPostID)
		}

		if err != nil {
			return fmt.Errorf("err resolve source: %w", err)
		}

		post, err = feed.InsertFeed(ctx, tx, f)
		if err != nil {
			return fmt.Errorf("err insert post: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO post_links (from_post_id, to_post_id, relation, metadata)
			VALUES ($1, $2, 'discovered', jsonb_build_object('via', $3::text))
			`, post.ID, link.FromPostID, link.Via); err != nil {
			return fmt.Errorf("err link discovered: %w", err)
		}

		if _, err := tx.Exec(ctx, `INSERT INTO tag_checked (post_id) VALUES ($1)`, post.ID); err != nil {
			return fmt.Errorf("err mark tag checked: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE link_events
			SET status = 'accepted', domain_id = $2, gate_scores = $3, error = NULL, attempts = attempts + 1, checked_at = now()
			WHERE id = $1
			`, link.ID, f.DomainID, scores); err != nil {
			return fmt.Errorf("err accept link: %w", err)
		}

		return nil
	})

	return
}

func proposeSource(ctx context.Context, tx pgx.Tx, domainID, fromPostID string) (id string, err error) {
	// enabled defaults to false, so it must be set or the source could never be promoted into scraping.
	if err = tx.QueryRow(ctx, `
		INSERT INTO sources (domain_id, adapter, url, enabled, origin, status)
		SELECT id, 'page', base_url, true, 'discovered', 'proposed' FROM domains WHERE id = $1
		RETURNING id
		`, domainID).Scan(&id); err != nil {
		return "", fmt.Errorf("err propose source: %w", err)
	}

	if _, err = tx.Exec(ctx, `INSERT INTO source_discovered_by_post (source_id, post_id) VALUES ($1, $2)`, id, fromPostID); err != nil {
		return "", fmt.Errorf("err record discovering post: %w", err)
	}

	return id, nil
}
```

- [ ] **Step 5: Ingester**

Create `internal/service/ingest/ingest.go`:

```go
package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dagulv/kharoborathus/internal/lib/schedule"
	"github.com/dagulv/kharoborathus/internal/service/domain"
	"github.com/dagulv/kharoborathus/internal/service/feed"
	"github.com/dagulv/kharoborathus/internal/service/robot"
	"github.com/dagulv/kharoborathus/internal/service/robot/adapter"
	"github.com/dagulv/kharoborathus/internal/service/source"
	sourceadapter "github.com/dagulv/kharoborathus/internal/service/source/adapter"
	"github.com/dagulv/kharoborathus/internal/service/switches"
	"github.com/dagulv/kharoborathus/internal/service/tagging"
	"github.com/jackc/pgx/v5"
)

const (
	tickInterval = time.Minute
	batchSize    = 10
	dailyCap     = 200
	maxAttempts  = 3
	domainGap    = 10 * time.Second
	fetchTimeout = 20 * time.Second
)

type Ingester struct {
	store      *Store
	switches   *switches.Store
	tags       *tagging.Store
	feeds      *feed.Store
	domains    *domain.Store
	trim       *source.Trim
	tagger     *robot.Tagger
	summarizer *robot.Summarizer
	client     *http.Client
	pacer      *pacer
	robots     *Robots
	l          *slog.Logger
}

type result struct {
	status   string
	domainID string
	reason   string
	scores   any
	aiCalls  int
}

func NewIngester(s *Store, sw *switches.Store, tags *tagging.Store, feeds *feed.Store, domains *domain.Store, trim *source.Trim, tagger *robot.Tagger, summarizer *robot.Summarizer, l *slog.Logger) *Ingester {
	client := &http.Client{Timeout: fetchTimeout}
	p := newPacer(domainGap)

	return &Ingester{
		store:      s,
		switches:   sw,
		tags:       tags,
		feeds:      feeds,
		domains:    domains,
		trim:       trim,
		tagger:     tagger,
		summarizer: summarizer,
		client:     client,
		pacer:      p,
		robots:     NewRobots(client, p),
		l:          l,
	}
}

func (in *Ingester) Run(ctx context.Context) error {
	for range schedule.Always.Tick(ctx, tickInterval) {
		if err := in.tick(ctx); err != nil {
			in.l.Error("ingest links", "err", err)
		}
	}

	return ctx.Err()
}

func (in *Ingester) tick(ctx context.Context) error {
	on, err := in.switches.Enabled(ctx, switches.DiscoveredIngest)
	if err != nil || !on {
		return err
	}

	tags, err := in.tags.ListTags(ctx)
	if err != nil {
		return fmt.Errorf("err list tags: %w", err)
	}

	if !slices.ContainsFunc(tags, func(t robot.Tag) bool { return t.Gate }) {
		in.l.Debug("no gate tags")
		return nil
	}

	checked, err := in.store.CheckedLastDay(ctx)
	if err != nil || checked >= dailyCap {
		return err
	}

	links, err := in.store.ListPending(ctx, pendingWindow, min(batchSize, dailyCap-checked))
	if err != nil {
		return err
	}

	counts := make(map[string]int)
	aiCalls := 0

	defer func() {
		if len(links) > 0 {
			in.l.Info("ingest", "processed", len(links), "counts", counts, "aiCalls", aiCalls)
		}
	}()

	for _, link := range links {
		res, err := in.process(ctx, link, tags)
		aiCalls += res.aiCalls

		switch {
		case errors.Is(err, adapter.ErrUnavailable) || ctx.Err() != nil:
			return nil
		case err != nil:
			failed, err := in.store.Retry(ctx, link.ID, res.domainID, err.Error(), maxAttempts)
			if err != nil {
				return err
			}

			res.status = "retried"
			if failed {
				res.status = statusFailed
			}
		case res.status != statusAccepted:
			if err := in.store.Finish(ctx, link.ID, res.status, res.domainID, res.scores, res.reason); err != nil {
				return err
			}
		}

		counts[res.status]++
	}

	return nil
}

// process returns the link's final status, or an error to retry it on a later tick.
func (in *Ingester) process(ctx context.Context, link Link, tags []robot.Tag) (res result, err error) {
	allowed, err := in.robots.Allowed(ctx, link.URL)
	if err != nil {
		return res, err
	}

	if !allowed {
		res.status, res.reason = statusFailed, "disallowed by robots.txt"
		return res, nil
	}

	if err := in.pacer.Wait(ctx, hostOf(link.URL)); err != nil {
		return res, err
	}

	page, err := sourceadapter.FetchHTML(ctx, in.client, link.URL)
	if err != nil {
		return res, err
	}

	d, err := in.domains.GetOrCreateDomain(ctx, link.URL)
	if err != nil {
		return res, fmt.Errorf("err get domain: %w", err)
	}

	res.domainID = d.ID

	f := in.trim.Apply(ctx, d, feed.Feed{URL: link.URL, DomainID: d.ID, Lang: pageLang(page)}, page)

	if f.Content == "" {
		res.status, res.reason = statusRejected, "no content"
		return res, nil
	}

	if f.Title == "" {
		res.status, res.reason = statusRejected, "no title"
		return res, nil
	}

	f, err = source.Markdown(f)
	if err != nil {
		return res, err
	}

	duplicate, err := in.feeds.HasDuplicate(ctx, f.Content)
	if err != nil {
		return res, err
	}

	if duplicate {
		res.status = statusDuplicate
		return res, nil
	}

	res.aiCalls++

	v, err := in.tagger.Gate(ctx, f.Title, f.Content, tags)
	if err != nil {
		return res, fmt.Errorf("err gate: %w", err)
	}

	res.scores = v

	if !v.Passed {
		res.status = statusRejected
		return res, nil
	}

	res.aiCalls++

	if summary, err := in.summarizer.Summarize(ctx, f.Content); err == nil {
		f.ShortContent = summary
	} else if !errors.Is(err, adapter.ErrUnavailable) {
		in.l.Warn("summarize", "url", link.URL, "err", err)
	}

	f.Tags = v.Keys

	_, err = in.store.Accept(ctx, link, f, v)
	if errors.Is(err, pgx.ErrNoRows) {
		res.status, res.reason = statusDuplicate, "url exists"
		return res, nil
	}

	if err != nil {
		return res, err
	}

	res.status = statusAccepted

	return res, nil
}

func pageLang(page []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return ""
	}

	lang, _ := doc.Find("html").Attr("lang")

	return lang
}
```

- [ ] **Step 6: Run the check**

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" ingest_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d ingest_check' < internal/migrations/schema.sql
(set -a; . ./.env; INGEST_CHECK_CONN="postgresql://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:5432/ingest_check?sslmode=disable" go test ./internal/service/ingest/ -run Check -count=1 > "$SCRATCHPAD/ingest_check.log" 2>&1; echo exit $?)
tail -40 "$SCRATCHPAD/ingest_check.log"
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" ingest_check'
```

Expected: `exit 0`, `ok`. If a fixture page shows `trim failed checks` in the log, change the fixture HTML, not the trimmer.

- [ ] **Step 7: Wire the worker**

In `cmd/main.go`, after `tagMerger := robot.NewTagMerger(classifier)`:

```go
	ingester := ingest.NewIngester(&ingestStore, &switchStore, &taggingStore, &feedStore, &domainStore, trim, tagger, summarizer, l.WithGroup("ingest"))
```

and after the link extraction `g.Go` block:

```go
	g.Go(func() error {
		return ingester.Run(ctx)
	})
```

- [ ] **Step 8: Delete the check, build and test**

```bash
rm internal/service/ingest/check_test.go
gofmt -l internal cmd
go build ./... && go vet ./... && go test ./...
```

Expected: no files listed, all ok, and `git status --short` lists only the files of this task.

- [ ] **Step 9: Commit**

```bash
git add internal/migrations/schema.sql internal/service/switches/store.go internal/service/ingest/model.go internal/service/ingest/store.go internal/service/ingest/ingest.go cmd/main.go
git commit -m "Ingest queued links behind the discovered_ingest switch

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Admin

**Files:**

- Modify: `frontend/src/lib/types.ts`, `frontend/src/routes/admin/admin.remote.ts`
- Modify: `frontend/src/routes/admin/sources/+page.svelte`, `frontend/src/routes/admin/+page.svelte`, `frontend/src/routes/admin/switches/+page.svelte`

**Interfaces:**

- Consumes: `sources.status`, `sources.origin`, `link_events.status`, the `discovered_ingest` switch row.
- Produces: `getLinkQueue` remote query.

- [ ] **Step 1: Baseline**

Run: `pnpm -C frontend check && pnpm -C frontend lint`
Expected: 0 errors. Note any warnings that exist before this task, so new ones are recognisable.

- [ ] **Step 2: Types and queries**

In `frontend/src/lib/types.ts` add to `Source` after `enabled: boolean;`:

```ts
status: "proposed" | "active" | "dormant";
origin: "manual" | "discovered";
```

In `admin.remote.ts` `listSources`, add `s.status,` and `s.origin,` after `s.enabled,`.

Add after `getStats`:

```ts
export const getLinkQueue = query(async () => {
  const sql = db();
  const rows = await sql<{ status: string; count: number }[]>`
		SELECT status, count(*)::int AS count FROM link_events GROUP BY status
	`;

  return Object.fromEntries(rows.map((r) => [r.status, r.count])) as Record<
    string,
    number
  >;
});
```

In `listSwitches`, add a branch after the `shadow_gate` one:

```sql
				WHEN 'discovered_ingest' THEN (
					SELECT count(*) || ' gate-checked posts, '
						|| coalesce(round(avg(gate_passed::int) * 100) || '% pass', 'no pass rate') || ', '
						|| (SELECT count(*) FROM sources WHERE deleted_at IS NULL AND flagged_at IS NOT NULL) || ' flagged sources'
					FROM tag_checked
					WHERE gate_passed IS NOT NULL
				)
```

- [ ] **Step 3: Sources page**

In `sources/+page.svelte`, in the Status cell after the flagged badge block, add:

```svelte
						{#if source.status !== 'active'}
							<span class="rounded bg-blue-100 px-1.5 py-0.5 text-xs text-blue-700">{source.status}</span>
						{/if}
						{#if source.origin === 'discovered'}
							<span class="rounded bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-600">discovered</span>
						{/if}
```

Change the Sync condition `{#if !source.deletedAt}` to `{#if !source.deletedAt && source.status === 'active'}`.

- [ ] **Step 4: Overview page**

In `admin/+page.svelte` import `getLinkQueue` next to `getStats`, add:

```ts
const linkQueue = $derived(await getLinkQueue());

const LINK_STATUSES = [
  "pending",
  "accepted",
  "rejected",
  "duplicate",
  "failed",
];
```

and before `<div class="mt-6 flex gap-4 text-sm">`:

```svelte
<h2 class="mt-8 mb-2 font-medium">Link queue</h2>

<div class="grid grid-cols-2 gap-4 sm:grid-cols-5">
	{#each LINK_STATUSES as status (status)}
		<div class="rounded border border-zinc-200 bg-white p-4">
			<div class="text-sm text-zinc-500 capitalize">{status}</div>
			<div class="text-2xl font-semibold tabular-nums">{linkQueue[status] ?? 0}</div>
		</div>
	{/each}
</div>
```

- [ ] **Step 5: Switch description**

In `switches/+page.svelte` add to `descriptions`:

```ts
discovered_ingest: "Fetches queued links, gates them and adds accepted pages as posts with proposed sources. Link extraction fills the queue either way.";
```

- [ ] **Step 6: Autofix and check**

Run the Svelte MCP `svelte-autofixer` on each changed `.svelte` file until it reports no issues. Then:

Run: `pnpm -C frontend check && pnpm -C frontend lint`
Expected: 0 errors and no warnings beyond the Step 1 baseline. Run `pnpm -C frontend format` only on the changed files if prettier complains about them.

- [ ] **Step 7: Verify the readiness SQL**

Build a throwaway database as in Task 8 Step 6, then run the `listSwitches` SQL from `admin.remote.ts` through psql with the `${}` parts removed:

```bash
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d ingest_check' <<'SQL'
SELECT s.key, CASE s.key
	WHEN 'shadow_gate' THEN (SELECT count(*) FROM tags WHERE enabled AND status = 'active' AND gate) || ' gate tags'
	WHEN 'discovered_ingest' THEN (
		SELECT count(*) || ' gate-checked posts, '
			|| coalesce(round(avg(gate_passed::int) * 100) || '% pass', 'no pass rate') || ', '
			|| (SELECT count(*) FROM sources WHERE deleted_at IS NULL AND flagged_at IS NOT NULL) || ' flagged sources'
		FROM tag_checked
		WHERE gate_passed IS NOT NULL
	)
END AS readiness
FROM switches s ORDER BY s.key;
SELECT status, count(*)::int AS count FROM link_events GROUP BY status;
SQL
```

Drop the database afterwards. Expected: `discovered_ingest | 0 gate-checked posts, no pass rate, 0 flagged sources`, `shadow_gate | 0 gate tags`, and an empty link queue result.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/lib/types.ts frontend/src/routes/admin/admin.remote.ts frontend/src/routes/admin/sources/+page.svelte frontend/src/routes/admin/+page.svelte frontend/src/routes/admin/switches/+page.svelte
git commit -m "Show source status, link queue and ingest readiness in admin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Knowledge base

**Files:**

- Modify: `docs/kb/link-extraction.md`, `docs/kb/discovered-ingest.md`, `docs/kb/trust-model.md`, `docs/kb/source-lifecycle.md`, `docs/kb/classifier-gate.md`, `docs/kb/post-linking.md`, `docs/kb/rollout.md`, `docs/kb/architecture.md`, `docs/kb/index.md`

- [ ] **Step 1: Link extraction**

Status `done`. Replace Current state with:

```markdown
[extract.go](../../internal/service/ingest/extract.go) scans trusted posts every minute and queues their links. It runs whether or not `discovered_ingest` is on. Parsing and filters live in [links.go](../../internal/service/ingest/links.go).
```

Add to Design after step 6:

```markdown
Rules:

- Only posts created inside the 7-day pending window are scanned. Older posts would queue stale links with a fresh `created_at`.
- Image links are not links. A linked image `[![alt](img)](href)` is skipped too.
- Own domain compares hosts, lowercased, without `www.` and scheme.
```

- [ ] **Step 2: Discovered ingest**

Status `done`. Replace Current state with:

```markdown
[ingest.go](../../internal/service/ingest/ingest.go) runs every minute while `discovered_ingest` is on. Admin shows `link_events` status counts on the overview.
```

Add after step 8:

```markdown
Rules:

- One link per host per tick, fresh links before retries. The 10-second pacer only separates a host's `robots.txt` fetch from its page fetch.
- The daily cap is a rolling 24 hours of `checked_at`.
- `robots.txt` disallow sets `failed` without retries, since it is no gate decision. A missing `robots.txt` allows everything. 5xx, 429 and network errors retry like a failed fetch. Results are cached per host for 24 hours.
- No enabled, active gate tag means no tick. Otherwise every link would be rejected for good.
- Classifier unavailable stops the tick and leaves the link untouched.
- Failed trimmer checks and short content are one check: `Trim.Apply` leaves no content, and the link is rejected with `no content`. A missing title rejects with `no title`.
- A URL conflict on insert sets `duplicate` with `url exists`.
- The oldest source includes deleted ones, so a domain whose source you deleted is not proposed again.
- `source_discovered_by_post` records the referring post once, when the proposed source is created.
- Language comes from `<html lang>`.
- Accepted posts do not touch `tags.last_matched_at`.
```

- [ ] **Step 3: Trust model**

Status `done`. Replace Current state with:

```markdown
`sources.origin` and `sources.status` exist. Only `active` sources are scraped, and manual sync of other sources returns 409. Link extraction reads trusted posts only. Tag rates and the broadness sample count trusted posts only. Discovered posts are marked `tag_checked` on insert, so they never propose tags.
```

Replace the Schema changes block with a line saying `origin` is in [schema.sql](../../internal/migrations/schema.sql).

- [ ] **Step 4: Source lifecycle**

Status `propose done, rest designed`. Replace Current state with:

```markdown
`status` and `origin` exist on sources. [Discovered ingest](discovered-ingest.md) creates proposed sources, and the source worker scrapes only `enabled` sources with `status = 'active'`. Promotion, demotion, dormancy and probe wait for phase 6 of [rollout](rollout.md), together with `status_changed_at` and `fetch_failures`.
```

- [ ] **Step 5: Classifier gate**

Status `done`. In Current state replace the sentence `The hard gate is not built. It ships with [discovered ingest](discovered-ingest.md) in phase 4.` with:

```markdown
The hard gate is `Tagger.Gate` in [gate.go](../../internal/service/robot/gate.go), run by [discovered ingest](discovered-ingest.md). The article question rides in the first Jev batch.
```

- [ ] **Step 6: Post linking**

In Current state replace `- \`discovered\` waits for phase 4 of [rollout](rollout.md).` with:

```markdown
- `discovered` is written by `Accept` in [ingest/store.go](../../internal/service/ingest/store.go).
```

- [ ] **Step 7: Rollout**

Replace the Current state paragraph with:

```markdown
Phases 1 to 4 are done, with the switches table and admin page. `shadow_gate` and `discovered_ingest` start off. See [architecture](architecture.md) for the system today.
```

- [ ] **Step 8: Architecture**

Replace `Four workers run side by side from [main.go](../../cmd/main.go).` with `Workers run side by side from [main.go](../../cmd/main.go).` Add rows to the worker table:

```markdown
| Links | [extract.go](../../internal/service/ingest/extract.go) | Queues outbound links from trusted posts into `link_events`. |
| Ingest | [ingest.go](../../internal/service/ingest/ingest.go) | Fetches, gates and inserts queued links while `discovered_ingest` is on. Proposes sources for new domains. |
```

Change the Sources row job to `Polls enabled, active sources at their \`rate\` and turns articles into posts.` Replace the Unused parts list with:

```markdown
- `discovery_events` is read only for `same_event` links. Nothing searches it yet.
- `link_events.discovery_events_id` and `via = 'search'` wait for [search discovery](search-discovery.md).
- `post_links` gets `discovered`, `duplicate_of` and `same_event`.
```

- [ ] **Step 9: Index**

Set statuses: Trust model `done`, Classifier gate `done`, Link extraction `done`, Discovered ingest `done`, Source lifecycle `propose done`.

- [ ] **Step 10: Check links and commit**

Run: `grep -rn "](../../" docs/kb | grep -o "](../../[^)]*)" | sort -u | sed 's/](..\/..\///; s/)$//' | while read p; do [ -e "$p" ] || echo "missing $p"; done`
Expected: no output.

```bash
git add docs/kb
git commit -m "Describe shipped link extraction and discovered ingest in kb

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
