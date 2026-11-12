# Backfill on source creation

## Intent

A feed shows only a source's newest posts. Some sites expose their history in other ways. A WordPress site, for one, serves its whole archive through the REST API. Every new source should read its history once, through whatever strategy its adapter supports.

The backfill was dropped in the core and adapter refactor. What remains:

- `FindWordPress` and the `wordpress` fetcher in `internal/adapter/fetcher/wordpress.go`.
- The `wordpress` entry in the `fetchers` map in `cmd/main.go`, a source type that no `sources` row uses.
- Nothing sends a source to it, and `sources.backfilled_at` is gone.

This design brings it back as an event: creating a source, or pressing a button on it, starts a backfill. It also makes source ingestion need nothing but plain HTTP requests, so no fetch waits on DeepSeek, Jev or Brave.

## Goals

- A new source backfills automatically, right after it is created.
- An admin can backfill any source again with a button.
- Backfill is a strategy of the `rss` adapter, not a source type. New strategies slot in without a new adapter.
- Every request to a host goes through the shared `pace.Pacer`.
- Every post's article page is fetched, for every adapter. Extract keeps the better of the page's content and the feed's content.
- Each post is inserted on its own, as soon as its page is fetched. No transaction spans a listing or a backfill.
- Site fetches have no count cap. Only external APIs are limited.
- Event-driven with Go channels. No polling for backfill work, no Postgres LISTEN or NOTIFY.

## Non-goals

- Schema changes. Backfill keeps no state of its own.
- Resuming an interrupted backfill by itself. The button reruns it.
- Backfilling existing sources automatically. The button covers them.
- The rate check in `sourceStream`.
- Strategies other than WordPress.
- Backfill for `page` sources.
- Storing listing bodies.
- Search for old posts. It will likely matter later, so the gate below is one condition that is easy to lift.
- A Jev cap. Jev stays uncapped, limited only by its availability.

## Anti-goals

- A backfill must never pause its source's own rate-checked fetches. Sharing the host's pacer slots with them is fine.
- A backfill must never hold up live sources on other hosts.
- No fetch step may call DeepSeek, Jev or Brave inline.

## Design

### Trigger

One endpoint in `internal/core/service/source/route.go`:

```
POST /admin/sources/{id}/backfill
```

1. Load the source. `404` when it does not exist.
2. `409` when it is not `active` or not enabled, like `syncSource`.
3. Send it on the backfill channel without blocking. `202` on success, `503` on a full channel.

Two callers in `frontend/src/routes/admin/admin.remote.ts`:

- `createSource` adds `RETURNING id` to its insert and calls the endpoint after the transaction commits. A source added by hand is `enabled` and `active` from its insert, so the endpoint accepts it and the backfill starts at once. A failed call is logged and does not fail the form. The button covers it.
- `reviewSource` calls the endpoint when it activates a source that was `proposed`. That is the first time the source is scraped. A `flagged` source made active again does not backfill.
- A new `backfillSource` command, next to `syncSource`, behind a "Backfill" button on each source in `frontend/src/routes/admin/sources/+page.svelte`.

### Backfill strategies

Backfill belongs to an adapter, not a source type. An adapter that can read history implements an optional interface in `internal/core/port/fetcher.go`:

```go
type Backfiller[T any] interface {
	Backfill(ctx context.Context, l *slog.Logger, url string) (<-chan T, error)
}
```

`Backfill` returns the source's history in the adapter's own item type. The items then run through that adapter's `Filter`, `FetchPost` and `ProcessFeed`, the same as feed items.

`rss` implements it with an ordered list of strategies:

```go
// archive streams a feed's history. ok is false when the site does not support it.
type archive func(ctx context.Context, l *slog.Logger, url string) (items <-chan rssDocument, ok bool, err error)
```

- `Backfill` tries each strategy in order. It uses the first one that reports `ok`.
- When none does, it returns `ErrNoArchive`. That is logged at info.
- An error from a strategy ends the backfill and is logged.

The first and only strategy is WordPress:
1. `FindWordPress` with the rss adapter's pacer on the feed URL. Its requests log as type `wordpress`.
2. No API, or a 401 or 404: `ok` is false.
3. Otherwise page through `wp/v2/posts`, 20 posts per request, newest first, each page paced. Each post is sent as an `rssDocument` as soon as it is read. Its `Lang` is the language `FindWordPress` found.

The `wordpress` adapter, its `fetchers` entry in `cmd/main.go` and `NewWordPress` go away. `FindWordPress` and the API paging stay in `internal/adapter/fetcher/wordpress.go`, as rss strategy code.

`page` implements no `Backfiller` for now, so a `page` source does not backfill. Pointing the WordPress strategy at a page URL is a later change, since `FindWordPress` already reads page heads.

Later strategies, such as RFC 5005 feed paging or a sitemap, are new entries in the list.

### Backfill lanes

`Run` in `sourced.go` builds a second plugin for each adapter that implements `Backfiller`. That plugin's first stage calls `Backfill` instead of `FetchFeed`, and it has its own workers.

- A second `route` goroutine reads the backfill channel and sends each source to the backfill plugin for its adapter.
- An adapter with no `Backfiller` logs "no backfill strategy" and drops the source.
- The source is sent unchanged, since its `ID`, `URL` and `Lang` are what the strategy needs.

- The backfill route never goes through `sourceStream`, so `dispatch` never sets a pause. The source keeps its own rate.
- The live `route` never sees a backfill. A backfill worker stays busy for a whole archive, so with all its workers busy, the backfill route blocks. Only backfills wait, and the endpoint answers `503` once its channel is full.
- `Filter` skips posts already in `raw_posts`, so a rerun fetches only what is missing. A rerun of a 400-post archive costs 20 API requests, about 100 s.
- A page that fails ends the backfill. The button reruns it.

### Always fetch the article page

`rss.FetchPost` fetches the article page for every item, not only excerpt items. The `excerpt` flag goes away. WordPress archive items run through the same `FetchPost`, so they get this too.

`ProcessFeed` sets `RawContent` to the page and keeps the feed's title, content, authors and date.

`Service.extract` in `extract.go` changes one rule: the page's extracted content replaces the feed's content only when it is longer in plain-text runes. Fields the feed gave still win, as now.

A failed page fetch keeps the feed's content with `RawContent` empty, where today the post is skipped. Then `Extract` runs on the feed content as it does for full-content feeds today.

### No inline DeepSeek in the fetch path

`linksFromOneListing` in `internal/adapter/extract/html/collect.go` calls DeepSeek inline, inside the `page` plugin's `FetchFeed`. It moves into the extractor's own goroutine, like `collectListing`. The listing that started it gets no links. The next rate tick fetches the listing again and uses the saved rule.

Extraction needs no rule. With none, `content()` falls back to `contentGeneric`, and the other fields fall back to meta tags.

### Search for old posts

`Discover` in `internal/adapter/search/discover.go` creates no new search query for a post whose posted date is older than `dedupeWindow`, 7 days. It treats it like an untrusted post: it still links the post to a recent query for the same event, so it joins that event's group, but it never starts a Brave search.

- A post with no posted date counts as new.
- Outbound links are unchanged. Discovered ingest already rejects linked pages older than 7 days, before any Jev call.
- Quality and enrich run on old posts as on any other.

### Discovered ingest

Drop `dailyFetchCap` and `CheckedLastDay` from `internal/core/service/source/discovered.go`. The 7-day window and the pacer stay. Jev stays uncapped. `maxPendingResults` in `search.go` stays at 200.

## Known tradeoffs

- **One request per post.** Every adapter fetches the article page. A feed with 20 new items on one host takes about 100 s at 5 s apart. Stored posts are never fetched again.
- **Long archives.** 400 posts take about 33 minutes. 10,000 posts take about 14 hours. There is no cap, by design.
- **No record of a backfill.** Admin cannot see whether a source was backfilled. Only the `wordpress` rows in `logs` show it.
- **A crash ends a backfill.** The button reruns it.
- **A new `page` source may wait a tick or more for its first posts.** Until its link rule is saved off-peak.
- **No fairness across hosts inside a plugin.** A plugin's 5 post workers share one queue. 20 new items on one busy host hold all 5 behind its pacer for about 100 s. Two backfills at once share the `rss` backfill workers the same way, so a large archive slows a second one down. The `page` plugin works this way today.
- **A backfill slows its own source's live fetches a little.** They share the host's pacer slots, so each live request waits behind the few backfill requests already queued, up to about 30 s.

## Calls made without asking

- Backfills go through their own route and per-adapter plugins, outside `sourceStream` and the live `route`.
- `page` sources get no backfill for now.
- No strategy found is logged at info, not as an error.
- Activating a `proposed` source backfills it. Reactivating a `flagged` one does not.
- Old posts still join recent search queries, and a post with no posted date counts as new.
- Outbound links from old posts are kept.
- A failed backfill call does not fail the source form.
- Feed content wins only when it is longer than the page's extracted content.
- A failed page fetch falls back to the feed content instead of skipping the post.
- WordPress archive posts also fetch the article page, with the API content as fallback.

## Left to the builder

- Backfill channel buffer size.
- Button placement and wording, matching the Sync button.
- Tests. At least: the endpoint's status codes, the strategy order, `ErrNoArchive`, a backfill reaching the `rss` backfill plugin without a pause on its source, `rss.FetchPost` always fetching, the content choice, a failed page fetch falling back to feed content, the discovered ingest without its cap, and an old post creating no search query while still joining a recent one.
- Updating `docs/kb/backfill.md` and `docs/kb/discovered-ingest.md`.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Gap between requests to a host | 5 s | `DomainPace`, shared by every adapter. |
| Posts per WordPress API page | 20 | Fits under the 5 MB body cap. |
| Workers per plugin | 5 | `workers`, carried over. |
