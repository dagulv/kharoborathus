# Phase 3: Post Linking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Write the first `post_links`. Every new post stores a 64-bit simhash and links `duplicate_of` to the earliest near-identical post of the last 7 days. Every post whose events are extracted links `same_event` to the earliest post of each event in the last 7 days.

**Architecture:** A pure function `simhash` in the feed package hashes the post's plain text from 3-word shingles. `feed.Store.CreateFeed` stores it and, in the same transaction, inserts the `duplicate_of` link with one SQL statement. `discovery.Store.CompleteDiscovery` inserts the `same_event` links in its existing transaction, right after storing the post's events. The feed and admin stay unchanged.

**Tech Stack:** Go 1.27, pgx v5, Postgres 18 (`bit_count`, `bigint::bit(64)`).

**Spec:** `docs/kb/post-linking.md`, `docs/kb/rollout.md` (phase 3), `docs/kb/search-discovery.md` (`discover_queue_event_idx`).

## Global Constraints

- Phase 3 writes `duplicate_of` and `same_event` only. `discovered` belongs to phase 4. `follows_up` and `translation_of` stay unused.
- Links point from the newer post to the older one. The older post anchors the group.
- Trusted duplicates are still inserted. Nothing is rejected or hidden in phase 3. The feed is unchanged.
- Shingle size 3 words. Max Hamming distance 3 of 64 bits. Window 7 days.
- `duplicate_of` metadata: `distance` in bits. `same_event` metadata: `entity`, `action`.
- Event matching uses lowercased `entity` and `action`.
- No switch. Phase 3 is not in the switch table.
- No backfill job. The user dropped it, since databases are recreated from `schema.sql`. Task 4 removes the bullet from `rollout.md`.
- Schema changes go straight into `internal/migrations/schema.sql`. No numbered migration file.
- Go style: `any` not `interface{}`, guard clauses, wrap leaf errors as `fmt.Errorf("err <action>: %w", err)`, comments only for gotchas.
- One commit per task. End each commit message with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.
- Do not reset the dev database. Verify SQL on a throwaway database built from `schema.sql`, then drop it.
- `$SCRATCHPAD` in commands means the session scratchpad directory. Check files written there are never committed.

## Plan rulings against the spec

These fill gaps the spec leaves open. Task 4 writes them back into the kb.

- **Newer and older mean insert order, `created_at`.** `posted_at` comes from feeds and can be missing or wrong, and ordering by it could make a new post the anchor of an existing group. The window is also measured on `created_at`, relative to the new post.
- **Plain text means the markdown `content` with link targets removed.** `[text](url)` keeps `text`. Words are runs of letters and digits, lowercased. Copies of a wire article on two sites differ in their link URLs, not their words.
- **No words means no simhash.** Content without a letter or digit stores NULL and is never linked. Otherwise every image-only post would be a duplicate of every other.
- **Fewer than 3 words form a single shingle.**
- **The simhash is stored as the uint64 bit pattern in a signed `BIGINT`.** Only bit patterns are compared, so negative values are fine.
- **`UpdateFeed` recomputes the simhash** like it recomputes `content_hash`. It has no callers and does not relink.
- **`discover_queue_event_idx` ships now**, with the definition from search-discovery, since post linking is its first reader.

## Review Focus

- Two copies of the same article whose only difference is link URLs, letter case or punctuation must get the same simhash. Pinned in Task 1.
- Content with no words, like an image-only post, must store NULL and never link, and must not panic. Pinned in Task 1 and Task 2 (post `z`).
- A simhash with the top bit set must survive the int64 round trip and still match its neighbours. Pinned in Task 2 (posts `m` and `n`).
- A post must link to the earliest match, not the closest one, and never to a post outside the window or newer than itself. Pinned in Task 2 (posts `c`, `d`, `y`) and Task 3 (posts `p1`, `p4`).
- A post with two events that resolve to the same anchor, like `Volvo Cars` and `VOLVO CARS`, must insert one link, not fail. Pinned in Task 3 (post `p4`).

---

### Task 1: Simhash

**Files:**
- Create: `internal/service/feed/simhash.go`
- Test: `internal/service/feed/simhash_test.go`

**Interfaces:**
- Produces: `func simhash(text string) (uint64, bool)` in package `feed`. The bool is false when the text has no words. Task 2 calls it.

- [ ] **Step 1: Write the failing test**

Create `internal/service/feed/simhash_test.go`:

```go
package feed

import (
	"math/bits"
	"strings"
	"testing"
)

const article = `Volvo Cars tecknar avtal om att förvärva det svenska batteribolaget Northvolts fabrik i Göteborg. Affären omfattar anläggningen i Torslanda samt omkring 300 anställda som går över till Volvo Cars. Köpeskillingen uppgår till 1,2 miljarder kronor och betalas kontant vid tillträdet som väntas ske under det fjärde kvartalet. Enligt bolaget ska fabriken användas för att tillverka battericeller till kommande elbilsmodeller. Affären är villkorad av godkännande från konkurrensmyndigheter i EU. Volvo Cars vd säger i ett pressmeddelande att förvärvet stärker bolagets kontroll över leveranskedjan och minskar beroendet av asiatiska leverantörer. Northvolt har sedan konkursen i våras sökt köpare till sina olika verksamheter. Aktien steg med tre procent i tidig handel på Stockholmsbörsen efter beskedet. Analytiker bedömer att affären är positiv för bolaget på lång sikt men att investeringsbehovet de närmaste åren blir betydande. Fabriken har en kapacitet på flera gigawattimmar per år.`

const otherArticle = `Ericsson redovisar ett rörelseresultat för tredje kvartalet som överträffar analytikernas förväntningar. Nettoomsättningen minskade med fyra procent jämfört med samma period i fjol, främst på grund av svag efterfrågan i Nordamerika. Bruttomarginalen förbättrades dock till 47 procent tack vare kostnadsbesparingar. Bolaget upprepar sin prognos för helåret och räknar med en återhämtning under nästa år. Aktien handlades upp sju procent under förmiddagen.`

func distance(t *testing.T, a, b string) int {
	t.Helper()

	ha, ok := simhash(a)
	if !ok {
		t.Fatalf("no simhash for %q", a)
	}

	hb, ok := simhash(b)
	if !ok {
		t.Fatalf("no simhash for %q", b)
	}

	return bits.OnesCount64(ha ^ hb)
}

func TestSimhashSameText(t *testing.T) {
	cases := map[string]string{
		"identical": article,
		"case":      strings.ToUpper(article),
		"spacing":   strings.ReplaceAll(article, " ", "\n\n  "),
		"link":      strings.Replace(article, "Northvolts", "[Northvolts](https://a.example/x?y=1)", 1),
		"image":     article + "\n\n![](https://a.example/img.jpg)",
	}

	for name, text := range cases {
		if d := distance(t, article, text); d != 0 {
			t.Errorf("%s: distance %d", name, d)
		}
	}
}

func TestSimhashSmallEdit(t *testing.T) {
	cases := map[string]string{
		"one word": strings.Replace(article, "1,2 miljarder", "1,3 miljarder", 1),
		"byline":   "TT\n\n" + article + "\n\nPublicerad 08:12",
	}

	for name, text := range cases {
		if d := distance(t, article, text); d > 3 {
			t.Errorf("%s: distance %d", name, d)
		}
	}
}

func TestSimhashOtherText(t *testing.T) {
	if d := distance(t, article, otherArticle); d <= 10 {
		t.Errorf("distance %d", d)
	}
}

func TestSimhashShortText(t *testing.T) {
	if d := distance(t, "Två ord", "två, ord!"); d != 0 {
		t.Errorf("distance %d", d)
	}

	if d := distance(t, "Två ord", "Tre ord"); d == 0 {
		t.Error("different short texts share a simhash")
	}
}

func TestSimhashNoWords(t *testing.T) {
	for _, text := range []string{"", "  \n ", "-- ! ?", "![](https://a.example/img.jpg)"} {
		if _, ok := simhash(text); ok {
			t.Errorf("simhash for %q", text)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/feed/ -run TestSimhash -v`
Expected: FAIL to build with "undefined: simhash".

- [ ] **Step 3: Write minimal implementation**

Create `internal/service/feed/simhash.go`:

```go
package feed

import (
	"hash/fnv"
	"regexp"
	"strings"
	"unicode"
)

const shingleSize = 3

var linkTarget = regexp.MustCompile(`\]\([^)]*\)`)

func simhash(text string) (uint64, bool) {
	words := strings.FieldsFunc(strings.ToLower(linkTarget.ReplaceAllString(text, "]")), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	if len(words) == 0 {
		return 0, false
	}

	var weights [64]int

	for i := range max(len(words)-shingleSize+1, 1) {
		h := fnv.New64a()
		h.Write([]byte(strings.Join(words[i:min(i+shingleSize, len(words))], " ")))
		sum := h.Sum64()

		for b := range weights {
			if sum&(1<<b) != 0 {
				weights[b]++
			} else {
				weights[b]--
			}
		}
	}

	var out uint64

	for b, w := range weights {
		if w > 0 {
			out |= 1 << b
		}
	}

	return out, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/service/feed/ -run TestSimhash -v`
Expected: PASS, 5 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/service/feed/simhash.go internal/service/feed/simhash_test.go
git commit -m "Add simhash for post text

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 2: Store simhash and link duplicates

**Files:**
- Modify: `internal/migrations/schema.sql` (posts `simhash` column)
- Modify: `internal/service/feed/store.go` (`CreateFeed`, `UpdateFeed`)

**Interfaces:**
- Consumes: `simhash(text string) (uint64, bool)` from Task 1.
- Produces: `posts.simhash BIGINT`, `duplicate_of` rows in `post_links`. `CreateFeed` keeps its signature and still returns an error matching `sql.ErrNoRows` on a URL conflict.

- [ ] **Step 1: Add the column**

In `internal/migrations/schema.sql`, replace the line

```sql
  --simhash BIGINT NOT NULL,
```

with

```sql
  simhash BIGINT, -- NULL when the content has no words
```

- [ ] **Step 2: Verify the link SQL on a throwaway database**

Write `link_check.sql` in the scratchpad:

```sql
INSERT INTO domains (id, base_url) VALUES ('00000000-0000-0000-0000-000000000001', 'https://a.example');
INSERT INTO sources (id, domain_id, adapter, url) VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'rss', 'https://a.example/rss');

CREATE TEMP TABLE fixture (name TEXT, simhash BIGINT, age INTERVAL);
INSERT INTO fixture VALUES
  ('a', 0, '2 days'),
  ('b', 1, '1 day'),
  ('c', 7, '0'),
  ('d', 15, '0'),
  ('x', 1099511627775, '8 days'),
  ('y', 1099511627775, '0'),
  ('z', NULL, '0'),
  ('m', -1, '1 day'),
  ('n', -2, '0'),
  ('o', 0, '-1 day');

INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, content_hash, simhash, created_at)
SELECT '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'https://a.example/' || name, 'sv', name, '', '', '\x00', simhash, now() - age
FROM fixture;

PREPARE link(uuid, int, int) AS
INSERT INTO post_links (from_post_id, to_post_id, relation, metadata)
SELECT n.id, o.id, 'duplicate_of', jsonb_build_object('distance', bit_count((o.simhash # n.simhash)::bit(64)))
FROM posts n
JOIN posts o ON o.created_at < n.created_at AND o.created_at > n.created_at - $2 * interval '1 day'
WHERE n.id = $1
  AND bit_count((o.simhash # n.simhash)::bit(64)) <= $3
ORDER BY o.created_at
LIMIT 1;

DO $$
DECLARE p RECORD;
BEGIN
  FOR p IN SELECT id FROM posts LOOP
    EXECUTE format('EXECUTE link(%L, 7, 3)', p.id);
  END LOOP;
END $$;

SELECT f.title AS from, t.title AS to, l.metadata
FROM post_links l
JOIN posts f ON f.id = l.from_post_id
JOIN posts t ON t.id = l.to_post_id
ORDER BY f.title;
```

Run:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" link_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d link_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d link_check' < "$SCRATCHPAD/link_check.sql"
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" link_check'
```

Expected rows, and no others:

| from | to | metadata |
|---|---|---|
| b | a | `{"distance": 1}` |
| c | a | `{"distance": 3}` |
| d | b | `{"distance": 3}` |
| n | m | `{"distance": 1}` |
| o | a | `{"distance": 0}` |

Why: `c` and `d` share `created_at` with `y`, `z` and `n`, so they never link to each other. `c` matches `a` and `b` and takes the earliest. `d` is 4 bits from `a`. `y` only matches `x`, which is outside the window. `z` has no simhash. `a` has nothing older within distance. `o` is newest and links to the earliest exact copy.

- [ ] **Step 3: Store the simhash and link in CreateFeed**

In `internal/service/feed/store.go`, add the constants after the imports:

```go
const (
	duplicateWindowDays  = 7
	maxDuplicateDistance = 3
)
```

Replace `CreateFeed` with:

```go
func (s *Store) CreateFeed(ctx context.Context, feed Feed) (out Feed, err error) {
	if feed.PostedAt.IsZero() {
		feed.PostedAt = time.Now()
	}

	hash := sha256.Sum256([]byte(feed.Content))

	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		out, err = scanFeed(tx.QueryRow(ctx, `
			INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, tags, authors, posted_at, content_hash, simhash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], '{}'), COALESCE($9::text[], '{}'), $10, $11, $12)
			ON CONFLICT (url) DO NOTHING
			RETURNING `+feedColumns,
			feed.SourceID, feed.DomainID, feed.URL, feed.Lang, feed.Title, feed.ShortContent, feed.Content, feed.Tags, feed.Authors, feed.PostedAt, hash[:], textSimhash(feed.Content),
		))
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO post_links (from_post_id, to_post_id, relation, metadata)
			SELECT n.id, o.id, 'duplicate_of', jsonb_build_object('distance', bit_count((o.simhash # n.simhash)::bit(64)))
			FROM posts n
			JOIN posts o ON o.created_at < n.created_at AND o.created_at > n.created_at - $2 * interval '1 day'
			WHERE n.id = $1
				AND bit_count((o.simhash # n.simhash)::bit(64)) <= $3
			ORDER BY o.created_at
			LIMIT 1
			`, out.ID, duplicateWindowDays, maxDuplicateDistance); err != nil {
			return fmt.Errorf("err link duplicate: %w", err)
		}

		return nil
	})

	return
}
```

Add the helper below `CreateFeed`:

```go
// Stored as the uint64 bit pattern, so half of all hashes are negative. Only bits are compared.
func textSimhash(content string) *int64 {
	h, ok := simhash(content)
	if !ok {
		return nil
	}

	v := int64(h)

	return &v
}
```

In `UpdateFeed`, add `simhash = $6` to the `SET` list and `textSimhash(feed.Content)` as the sixth argument:

```go
	return scanFeed(s.db.QueryRow(ctx, `
		UPDATE posts
		SET
			title = $2,
			short_content = $3,
			content = $4,
			content_hash = $5,
			simhash = $6
		WHERE id = $1
		RETURNING `+feedColumns,
		feed.ID, feed.Title, feed.ShortContent, feed.Content, hash[:], textSimhash(feed.Content),
	))
```

- [ ] **Step 4: Build, vet and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all ok, no output from build or vet.

- [ ] **Step 5: Check CreateFeed against a throwaway database**

Write `create_check_test.go` in the scratchpad, copy it into `internal/service/feed/` for this step only, and delete it afterwards. It is never committed.

```go
package feed

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/dagulv/kharoborathus/internal/lib/database"
)

func TestCreateFeedCheck(t *testing.T) {
	ctx := context.Background()

	db, err := database.NewDatabase(ctx, os.Getenv("LINK_CHECK_CONN"))
	if err != nil {
		t.Fatal(err)
	}

	s := NewStore(db)

	var sourceID, domainID string
	if err := db.QueryRow(ctx, `
		WITH d AS (INSERT INTO domains (base_url) VALUES ('https://a.example') RETURNING id)
		INSERT INTO sources (domain_id, adapter, url) SELECT id, 'rss', 'https://a.example/rss' FROM d RETURNING id, domain_id
		`).Scan(&sourceID, &domainID); err != nil {
		t.Fatal(err)
	}

	post := func(url, content string) Feed {
		f, err := s.CreateFeed(ctx, Feed{SourceID: sourceID, DomainID: domainID, URL: url, Lang: "sv", Title: url, Content: content})
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}

		return f
	}

	first := post("https://a.example/1", "Volvo Cars köper Northvolts fabrik i Göteborg för 1,2 miljarder kronor.")
	copied := post("https://b.example/1", "VOLVO CARS köper [Northvolts](https://b.example/nv) fabrik i Göteborg för 1,2 miljarder kronor!")
	post("https://a.example/2", "![](https://a.example/img.jpg)")
	post("https://a.example/3", "![](https://a.example/img2.jpg)")

	if _, err := s.CreateFeed(ctx, Feed{SourceID: sourceID, DomainID: domainID, URL: "https://a.example/1", Lang: "sv", Title: "again", Content: "x"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("conflict err %v", err)
	}

	rows, err := db.Query(ctx, `SELECT from_post_id, to_post_id, metadata->>'distance' FROM post_links`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var n int
	for rows.Next() {
		var from, to, distance string
		if err := rows.Scan(&from, &to, &distance); err != nil {
			t.Fatal(err)
		}

		n++

		if from != copied.ID || to != first.ID || distance != "0" {
			t.Errorf("link %s -> %s distance %s", from, to, distance)
		}
	}

	if n != 1 {
		t.Errorf("%d links", n)
	}

	var nulls int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM posts WHERE simhash IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}

	if nulls != 2 {
		t.Errorf("%d posts without simhash", nulls)
	}
}
```

The `db` service reads `.env` and listens on `127.0.0.1:5432`. Run without printing the password:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" link_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d link_check' < internal/migrations/schema.sql
cp "$SCRATCHPAD/create_check_test.go" internal/service/feed/
(set -a; . ./.env; LINK_CHECK_CONN="postgresql://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:5432/link_check?sslmode=disable" go test ./internal/service/feed/ -run TestCreateFeedCheck -v)
rm internal/service/feed/create_check_test.go
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" link_check'
```

Expected: PASS. `git status` shows no `create_check_test.go`.

- [ ] **Step 6: Commit**

```bash
git add internal/migrations/schema.sql internal/service/feed/store.go
git commit -m "Store post simhash and link duplicates on insert

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 3: Link same-event posts

**Files:**
- Modify: `internal/migrations/schema.sql` (`discover_queue_event_idx`)
- Modify: `internal/service/discovery/store.go` (`CompleteDiscovery`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `same_event` rows in `post_links`. `CompleteDiscovery` keeps its signature.

- [ ] **Step 1: Add the index**

In `internal/migrations/schema.sql`, after `discover_queue_pending_idx`, add:

```sql
CREATE INDEX IF NOT EXISTS discover_queue_event_idx ON discover_queue (lower(entity), lower(action), searched_at);
```

- [ ] **Step 2: Verify the link SQL on a throwaway database**

Write `event_check.sql` in the scratchpad:

```sql
INSERT INTO domains (id, base_url) VALUES ('00000000-0000-0000-0000-000000000001', 'https://a.example');
INSERT INTO sources (id, domain_id, adapter, url) VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'rss', 'https://a.example/rss');

INSERT INTO posts (source_id, domain_id, url, lang, title, short_content, content, content_hash, created_at)
SELECT '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'https://a.example/' || name, 'sv', name, '', '', '\x00', now() - age
FROM (VALUES ('p0', interval '11 days'), ('p1', '3 days'), ('p2', '2 days'), ('p3', '1 day'), ('p4', '0')) f (name, age);

INSERT INTO discover_queue (post_id, entity, action, query)
SELECT p.id, e.entity, e.action, e.query
FROM (VALUES
  ('p0', 'Volvo Cars', 'acquisition', 'q0'),
  ('p1', 'Volvo Cars', 'acquisition', 'q1'),
  ('p2', 'volvo cars', 'Acquisition', 'q2'),
  ('p2', 'Ericsson', 'report', 'q3'),
  ('p3', 'Ericsson', 'report', 'q4'),
  ('p4', 'Volvo Cars', 'acquisition', 'q5'),
  ('p4', 'VOLVO CARS', 'ACQUISITION', 'q6')
) e (title, entity, action, query)
JOIN posts p ON p.title = e.title;

PREPARE link(uuid, int) AS
INSERT INTO post_links (from_post_id, to_post_id, relation, metadata)
SELECT n.id, anchor.id, 'same_event', jsonb_build_object('entity', e.entity, 'action', e.action)
FROM posts n
JOIN discover_queue e ON e.post_id = n.id
CROSS JOIN LATERAL (
  SELECT o.id
  FROM discover_queue q
  JOIN posts o ON o.id = q.post_id
  WHERE lower(q.entity) = lower(e.entity)
    AND lower(q.action) = lower(e.action)
    AND o.created_at < n.created_at
    AND o.created_at > n.created_at - $2 * interval '1 day'
  ORDER BY o.created_at
  LIMIT 1
) anchor
WHERE n.id = $1
ON CONFLICT (from_post_id, to_post_id, relation) DO NOTHING;

DO $$
DECLARE p RECORD;
BEGIN
  FOR p IN SELECT id FROM posts ORDER BY created_at LOOP
    EXECUTE format('EXECUTE link(%L, 7)', p.id);
  END LOOP;
END $$;

SELECT f.title AS from, t.title AS to, l.relation
FROM post_links l
JOIN posts f ON f.id = l.from_post_id
JOIN posts t ON t.id = l.to_post_id
ORDER BY f.title, t.title;
```

Run:

```bash
docker compose exec -T db sh -c 'createdb -U "$POSTGRES_USER" link_check'
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d link_check' < internal/migrations/schema.sql
docker compose exec -T db sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d link_check' < "$SCRATCHPAD/event_check.sql"
docker compose exec -T db sh -c 'dropdb -U "$POSTGRES_USER" link_check'
```

Expected rows, and no others, all `same_event`:

| from | to |
|---|---|
| p2 | p1 |
| p3 | p2 |
| p4 | p1 |

Why: `p1` has no earlier match inside 7 days, since `p0` is 8 days older. `p2` matches `p1` despite different case and has no earlier `Ericsson` post. `p4` has two events that resolve to `p1` and inserts once.

- [ ] **Step 3: Link in CompleteDiscovery**

In `internal/service/discovery/store.go`, add after the imports:

```go
const eventWindowDays = 7
```

In `CompleteDiscovery`, after the `INSERT INTO discover_queue` exec and still inside `if len(items) > 0`, add:

```go
			if _, err := tx.Exec(ctx, `
				INSERT INTO post_links (from_post_id, to_post_id, relation, metadata)
				SELECT n.id, anchor.id, 'same_event', jsonb_build_object('entity', e.entity, 'action', e.action)
				FROM posts n
				JOIN discover_queue e ON e.post_id = n.id
				CROSS JOIN LATERAL (
					SELECT o.id
					FROM discover_queue q
					JOIN posts o ON o.id = q.post_id
					WHERE lower(q.entity) = lower(e.entity)
						AND lower(q.action) = lower(e.action)
						AND o.created_at < n.created_at
						AND o.created_at > n.created_at - $2 * interval '1 day'
					ORDER BY o.created_at
					LIMIT 1
				) anchor
				WHERE n.id = $1
				ON CONFLICT (from_post_id, to_post_id, relation) DO NOTHING
				`, postID, eventWindowDays); err != nil {
				return fmt.Errorf("err link same event: %w", err)
			}
```

Add `"fmt"` to the imports.

- [ ] **Step 4: Build, vet and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all ok.

- [ ] **Step 5: Commit**

```bash
git add internal/migrations/schema.sql internal/service/discovery/store.go
git commit -m "Link same-event posts after event extraction

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 4: Describe shipped post linking in kb

**Files:**
- Modify: `docs/kb/post-linking.md`
- Modify: `docs/kb/architecture.md`
- Modify: `docs/kb/rollout.md`
- Modify: `docs/kb/search-discovery.md`
- Modify: `docs/kb/index.md`

- [ ] **Step 1: post-linking.md**

- Status: `done`.
- Replace Current state with:
  - `duplicate_of` is written by `CreateFeed` in [feed/store.go](../../internal/service/feed/store.go), `same_event` by `CompleteDiscovery` in [discovery/store.go](../../internal/service/discovery/store.go).
  - `discovered` waits for phase 4.
  - The feed does not read `post_links` yet.
- Under Same text, add the rulings: plain text is the markdown with link targets removed, words are lowercased runs of letters and digits, fewer than 3 words form one shingle, no words stores NULL and never links, the hash is stored as its bit pattern in a signed `BIGINT`.
- Under Rules, add: newer and older mean insert order, `created_at`, and the window is measured from the new post's `created_at`. Say why: `posted_at` can be missing or wrong and would move existing anchors.
- Schema changes: change `ALTER TABLE` to say the column is in `schema.sql`, and that `discover_queue_event_idx` shipped with this phase.

- [ ] **Step 2: architecture.md**

- Tagging and Discovery rows: Discovery also links `same_event` posts.
- Source pipeline step 8: "Insert into `posts` with a simhash and link `duplicate_of`."
- Unused parts of the schema: `post_links` now has writers for `duplicate_of` and `same_event`. Keep `source_discovered_by_post`.

- [ ] **Step 3: rollout.md**

- Current state: phases 1 to 3 are done.
- Delete the `**Phase 3:** a one-time job backfills ...` bullet.

- [ ] **Step 4: search-discovery.md and index.md**

- search-discovery Current state: add that `discover_queue_event_idx` exists and post linking reads it.
- index.md: Post linking status `done`.

- [ ] **Step 5: Commit**

```bash
git add docs/kb
git commit -m "Describe shipped post linking in kb

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
