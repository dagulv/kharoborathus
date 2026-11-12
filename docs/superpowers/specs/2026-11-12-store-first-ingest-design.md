# Store-first ingest and backfill

## Intent

Step 1 of the event chain, source ingestion, should need nothing but plain HTTP requests from the kharoborathus server and a database. Today it can wait on DeepSeek before anything is stored:

- A `page` source with no link rule gets no links. A first rule can come from one inline DeepSeek call that holds a pipeline worker, see `linksFromOneListing` in `internal/adapter/extract/html/collect.go`.
- The listing body is never stored, so a listing fetched while no rule exists is thrown away.
- The WordPress backfill was dropped in the core and adapter refactor. `FindWordPress` and the `wordpress` fetcher remain, but nothing sends a source to them and `sources.backfilled_at` is gone.

Extraction itself needs no external call. With no rule, `content()` falls back to `contentGeneric`, and title, authors and posted date fall back to meta tags. Rules only sharpen the result.

This design stores every listing first, streams its links one at a time, and inserts each post as soon as its page is fetched. Backfill becomes an event fired when a source is created, and streams its archive the same way.

## Goals

- Fetching a listing, finding its links, fetching each article and storing it never wait on DeepSeek, Jev or Brave.
- Every listing body is stored before its links are read.
- Each post is inserted on its own, as soon as its page is fetched. No transaction spans a listing or a backfill.
- Every adapter fetches the article page for every post. Extract decides whether the page or the feed's own content is the better text.
- A new source is checked for a WordPress REST API once, right after it is created. A found archive is read page by page, and the read resumes after a restart.
- All requests to one host go through the shared `pace.Pacer`.
- Site fetches have no count cap. Only external APIs are limited: Brave by its daily budget, DeepSeek by its off-peak schedule, Jev by its availability.
- The flow is event-driven with Go channels. No step polls the database for new work, and nothing uses Postgres LISTEN or NOTIFY.

## Non-goals

- The rate check in `sourceStream`. Its known gaps stay: pauses in memory, the pause set on dispatch, a blocking send, rate 0 and a leaking pause map.
- The quality check, enrich, discovery and search.
- Discovered ingest, except removing its `dailyFetchCap`.
- Extracting stored posts again when a better rule arrives.
- Archive APIs other than WordPress, such as Ghost or Substack.
- Following "next page" links on `page` listings.

## Anti-goals

- No step that makes plain HTTP requests may call DeepSeek, Jev or Brave inline.
- A link must never be lost because the process stopped while its listing was being read.
- A WordPress backfill must never starve live sources on other hosts.
- An adapter must never need a DeepSeek rule to find links when its format already marks them.

## Design

### Steps

| # | Step | Input | Persists | External API |
|---|---|---|---|---|
| 1a | Listing fetch | source | `listings` row, `pending` | none |
| 1b | Link | listing | nothing. Streams links on a channel | none. `page` needs a stored link rule |
| 1c | Post | link | one `raw_posts` row, `pending`, with `raw_content`, extracted `content` and simhash | none |
| 1d | Quality | raw post | `status` | Jev, unchanged |

When a listing's link channel is closed and every link it sent has been handled by step 1c, the listing is set to `linked`.

Link rule generation for `page` runs beside the chain, off-peak, and sends a rule event when it saves a rule.

### Linker

```go
type Link struct {
	URL  string
	Lang string
	Feed domain.RawPost // title, content, authors, posted date from the feed; zero for page
}

type Linker interface {
	Links(ctx context.Context, l *slog.Logger, listing domain.Listing) (<-chan Link, error)
}
```

`Links` returns at once and sends links as it finds them. It closes the channel when the listing has no more. An error before the first link means the listing could not be read.

| Adapter | Listing body | Links from | `Feed` fields |
|---|---|---|---|
| `page` | listing page HTML | the domain's `links` rule | none |
| `rss` | feed XML or JSON | `item.Link` from gofeed: RSS `<link>`, Atom `rel="alternate"`, JSON Feed `url` | title, content or description, authors, categories, published date, feed language |
| `wordpress` | one page of `wp/v2/posts` JSON | each post's `link` | title, `content.rendered`, author and term names, `date_gmt` |

`rss` and `wordpress` need no DeepSeek rule. Only `page` does.

`port.Fetcher` with `FetchFeed`, `Filter`, `FetchPost` and `ProcessFeed` is replaced by a listing fetch and a `Linker` per adapter. The `excerpt` flag in `rss.go` goes away.

### Why a listing is marked `linked` last

The `linked` mark is a finish line, not a transaction. Each post is committed on its own the moment its page is fetched. The listing row only records that every link it holds has been dealt with.

If the listing were marked when its links were found, a crash after link 5 of 20 would lose links 6 to 20:

- A live listing would find them again on the next rate tick, but only while they are still on the page.
- A backfill page is never fetched again, so they would be gone for good.

Marked last, a crash leaves the listing `pending`. The catch-up job sends it through step 1b again. Links already in `raw_posts` are skipped, so only links 6 to 20 are fetched.

What "handled" means for a link:
- its raw post was inserted, as `pending` or `failed`,
- or it was already in `raw_posts`.

A listing with 20 new links on one host is `pending` for about 100 s at 5 s apart. That is only how long its finish line takes. Its posts appear in `raw_posts` one by one during that time.

### Schema

A new file `002_store_first_ingest.sql`, since `migrations.go` says a shipped file is never edited. If no database has run `001_init.sql` yet, these can go into it instead.

```sql
CREATE TABLE IF NOT EXISTS listings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  source_id UUID NOT NULL REFERENCES sources (id),
  adapter TEXT NOT NULL, -- page | rss | wordpress
  url TEXT NOT NULL, -- after redirects
  domain_id UUID NOT NULL REFERENCES domains (id),
  lang TEXT NOT NULL DEFAULT '', -- <html lang> or the feed's <language>
  raw_content TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending', -- pending | linked | failed
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  linked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS listings_pending_idx ON listings (domain_id, created_at) WHERE status = 'pending';

ALTER TABLE sources ADD COLUMN IF NOT EXISTS backfilled_at TIMESTAMPTZ; -- checked for an archive
ALTER TABLE sources ADD COLUMN IF NOT EXISTS backfill_url TEXT; -- posts endpoint, NULL when none was found
ALTER TABLE sources ADD COLUMN IF NOT EXISTS backfill_pages INT;
ALTER TABLE sources ADD COLUMN IF NOT EXISTS backfill_page INT NOT NULL DEFAULT 0; -- last page linked
```

`raw_posts` is unchanged. A row exists only once its page was fetched, so `pending` keeps today's meaning: waiting for the quality check.

Backfill state follows from the source's columns:

| `backfilled_at` | `backfill_url` | Pages | State |
|---|---|---|---|
| NULL | | | Not checked |
| set | NULL | | No archive, or the check failed |
| set | set | `backfill_page < backfill_pages` | Reading |
| set | set | `backfill_page >= backfill_pages` | Done |

### Step 1a: Listing fetch

1. `sourceStream` dispatches a source as today.
2. The adapter waits on the pacer and GETs `source.URL`.
3. A failed request is logged and nothing is stored, as today. The next rate tick tries again.
4. Insert a `listings` row with the body, final URL, domain and language.
5. Send the listing to step 1b.

### Step 1b: Link

1. Call the adapter's `Linker`.
2. `page` with no `links` rule for the domain: hand the listing to the extractor as a link sample without waiting, close the channel at once and leave the listing `pending`. It does not count as linked. The rule event reruns it.
3. An error before the first link sets the listing `failed`.
4. For each link, skip it if its URL is already in `raw_posts`. Otherwise send it to step 1c with the listing's progress, see Runtime.
5. Keep links on the listing's host for `page`, as `applyLinks` does now. Keep every link for `rss` and `wordpress`.

### Step 1c: Post

One link at a time, each in its own statement:

1. Wait on the pacer and GET the link with an HTML accept header.
2. On success, run `Service.Extract` on the HTML. It uses the domain's rules when they exist and the generic fallback otherwise. A missing rule still hands the page to the extractor as a sample, as today.
3. Choose the content. Keep the page's extracted content unless the feed's content is longer in plain-text runes. Fields the feed gave win over extracted ones, as in `Service.extract` now.
4. On a failed fetch, use the feed's fields with `raw_content` empty.
5. Convert to markdown and compute the simhash.
6. Insert into `raw_posts` as `pending`. No content from the page or the feed inserts the row as `failed` instead, so the next rate tick does not fetch it again. A URL conflict counts as handled.
7. Send a `pending` row to step 1d. Mark the link handled.

### Step 1d: Quality

Unchanged. `checkQuality` and `saveStatus` as today. The duplicate check still works, since each row gets its simhash at insert and creation order equals extraction order.

### Link rule events

`htmlExtractor` takes a `chan<- string` of domain ids. After saving a `links` rule, it sends the domain id without blocking. A full channel drops it, and the catch-up job covers it.

The rule worker reads each domain id, lists that domain's `pending` listings oldest first, and sends each one to step 1b. That is a read triggered by an event, not polling.

`linksFromOneListing` keeps its one-listing first attempt, but never inline. Step 1b hands the listing over. Generation runs in the extractor's own goroutine during off-peak, then sends the rule event.

### Backfill

**Trigger.** The admin form `createSource` in `frontend/src/routes/admin/admin.remote.ts` returns the new source id and calls `POST /admin/sources/{id}/backfill` on the Go app, like `syncSource`. The handler checks that the source exists, is enabled and active, then sends it on the backfill channel without blocking. It answers `202`, or `503` on a full channel. The source form still succeeds when the call fails. The catch-up job covers it.

**Check.**
1. Skip a source with `backfilled_at` set, unless it is still reading.
2. `FindWordPress` with the shared pacer on `source.URL`, which works for a page or a feed.
3. Set `backfilled_at`. On an API, also set `backfill_url`. Otherwise leave it NULL. A failed check is not retried. Clearing `backfilled_at` runs it again.

**Read.** The backfill `Linker` streams the whole archive, one page at a time, starting at `backfill_page + 1`:

1. Wait on the pacer and GET one page, 20 posts newest first, as `wordpress.page` does now. Set `backfill_pages` from `X-WP-TotalPages`.
2. Store the page as a `listings` row with adapter `wordpress` and the source's id.
3. Send its links on the channel, each tied to that page's listing.
4. Read the next page only once the previous page's links have all been sent. Sends block, so the reader stays at most one page ahead of the post step.
5. When a page's listing is marked `linked`, set `backfill_page` to that page. Pages finish in order, since the post step takes their links in order.
6. A page that fails stops the read and closes the channel. `backfill_page` stays at the last linked page, so the catch-up job resumes from there.

**Lane.** Backfill links go to their own post step workers, not the live ones. A 400-post archive means 400 paced fetches on one host, about 33 minutes at 5 s apart. Live sources on other hosts must not wait behind them. Rows from both lanes go to the same quality step.

The backfill never goes through `sourceStream`, so it never touches its source's rate pause. The `wordpress` entry in the `fetchers` map in `cmd/main.go` goes away. No `sources` row has adapter `wordpress`.

### Catch-up job

One goroutine, ticking every minute, alongside `retryPending`, which stays as it is. Each kind has its own batch and stops at its first error. It skips listings and backfills that this process is still reading, which it tracks in memory.

| Kind | Rows | Sent to |
|---|---|---|
| Unlinked listings | `pending`, adapter `rss`, or `page` with a `links` rule | step 1b, live lane |
| Stopped backfills | `backfill_url` set, `backfill_page < backfill_pages`, or pages not yet known | backfill channel |
| Unchecked sources | enabled, active, `backfilled_at` NULL | backfill channel |

A stopped backfill resumes from `backfill_page + 1`. Its `pending` `wordpress` listing from before the crash is linked again first, as an unlinked listing on the backfill lane.

A `page` listing waiting for a rule is not resent every minute. Its domain's rule event reruns it.

The first run after deploy checks every existing active source for a WordPress API.

### Discovered ingest

Drop `dailyFetchCap` and `CheckedLastDay` from `internal/core/service/source/discovered.go`. The 7-day window and the pacer stay. It already calls `Service.Extract` inline, which keeps working since extraction never waits.

### Retention

Once a day, a delete job removes:
- `linked` and `failed` listings older than 7 days,
- `pending` `page` listings older than 1 day, keeping the newest 3 per domain.

A live listing page is fetched again on every rate tick, so a day-old copy holds no link the newest one lacks. Three copies are what `listingSamples` needs for a link rule.

### Runtime

- Every link carries its listing's progress: a count of links sent, a count handled and whether the channel is closed. The post step marks a link handled. The last one to finish marks the listing `linked`. For a backfill page it also sets `backfill_page`.
- Sends between steps block, so a slow step pushes back on the one before it. Sends from HTTP handlers and the extractor never block. A full channel drops the item with a log line, and the catch-up job covers it.
- Live steps run `workers` goroutines, today 5. Backfill runs its own.

## Known tradeoffs

- **One request per post.** Every adapter fetches the article page. A feed with 20 new items on one host takes about 100 s at 5 s apart. Items already stored are never fetched again.
- **Live workers still wait on the pacer.** Five live post workers can all wait on one busy host. Today's pipeline has the same limit.
- **Large archives run long.** A site with 10,000 posts takes about 14 hours of paced fetches. There is no cap, by design.
- **A failed backfill check is final** until `backfilled_at` is cleared, as in the old design.
- **A failed fetch with no feed content is final.** A `page` link whose fetch fails once is inserted as `failed` and never fetched again.
- **Posts keep the extraction they got.** A post fetched before its domain has rules keeps the generic result.

## Calls made without asking

- Backfill posts also fetch the article page, to match "always fetch the article page". The API content is the fallback.
- Feed content wins only when it is longer than the page's extracted content.
- A no-content post is inserted as `failed`, where today it is dropped and fetched again every tick.
- Backfill state as four columns on `sources`, not a separate table.
- Backfill has its own post step lane, with 2 workers.
- Retention of 7 days for linked listings, and 1 day plus the newest 3 for pending `page` listings.
- The one-listing first link rule attempt stays, moved off-peak.
- The admin backfill call failing does not fail the source form.

## Left to the builder

- Package layout, following the core and adapter split.
- Channel buffer sizes, catch-up batch sizes and gaps.
- How listing progress is shared between steps, as long as the listing is marked only after its last link is handled.
- Whether `domain.Listing` lives in `internal/core/domain`.
- Tests. At least: each adapter's `Linker`, the content choice, a crash mid-listing resuming only the missing links, a backfill resuming at `backfill_page + 1`, the backfill lane leaving the live lane free, a link rule event rerunning pending listings, and each catch-up kind.
- Updating `docs/kb/backfill.md`, `docs/kb/discovered-ingest.md` and `docs/kb/architecture.md`.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Gap between requests to a host | 5 s | `DomainPace`, shared by every step. |
| Posts per WordPress API page | 20 | Fits under the 5 MB body cap. |
| Live workers per step | 5 | `workers`, carried over. |
| Backfill workers | 2 | Two archives at once on different hosts. |
| Listing retention | 7 days | Matches the duplicate and discovery windows. |
| Pending `page` listings kept | newest 3 | `listingSamples`. |
| Catch-up tick | 1 minute | Same as `retryPending`. |
