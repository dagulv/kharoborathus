# Backfill on source creation

## Intent

A feed or listing page shows only a source's newest posts. A WordPress site also serves its whole archive through the REST API. Every new source should read that archive once, whatever its adapter.

The backfill was dropped in the core and adapter refactor. What remains:

- `FindWordPress` and the `wordpress` fetcher in `internal/adapter/fetcher/wordpress.go`.
- The `wordpress` entry in the `fetchers` map in `cmd/main.go`.
- Nothing sends a source to it, and `sources.backfilled_at` is gone.

This design brings it back as an event: creating a source, or pressing a button on it, starts a backfill. It also makes source ingestion need nothing but plain HTTP requests, so no fetch waits on DeepSeek, Jev or Brave.

## Goals

- A new source backfills automatically, right after it is created.
- An admin can backfill any source again with a button.
- Backfill works for `rss` and `page` sources alike.
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
- Archive APIs other than WordPress.
- Storing listing bodies.

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

- `createSource` adds `RETURNING id` to its insert and calls the endpoint after the transaction commits. A failed call is logged and does not fail the form. The button covers it.
- `reviewSource` calls the endpoint when it activates a source that was `proposed`. That is the first time the source is scraped. A `flagged` source made active again does not backfill.
- A new `backfillSource` command, next to `syncSource`, behind a "Backfill" button on each source in `frontend/src/routes/admin/sources/+page.svelte`.

### Run

One goroutine reads the backfill channel:

1. `FindWordPress` with the shared pacer on `source.URL`. It finds the API from a page or a feed, so it works for `rss` and `page` sources. Its requests log as type `wordpress`.
2. No API, a 401 or 404, or an error: log it and stop.
3. Otherwise send a copy of the source with adapter `wordpress`, the posts endpoint as URL and the language `FindWordPress` found.

The goroutine's output channel is the `wordpress` plugin's input. `wordpress` leaves the `ins` map that `route` in `sourced.go` feeds, since no `sources` row has that adapter.

- The copies never go through `sourceStream`, so `dispatch` never sets a pause for them. The source keeps its own rate.
- They never go through `route` either. A `wordpress` worker stays busy for a whole archive, so with all 5 busy, a send blocks. Through `route` that would stall every live `rss` and `page` source. Outside it, only the backfill goroutine waits, and the endpoint answers `503` once its channel is full.

### Read

The existing `wordpress` plugin reads the copy:

- `FetchFeed` already returns a channel. It pages through the API, 20 posts per request, newest first, each page paced, and sends each post as it reads it. Nothing waits for the whole archive.
- `Filter` skips posts already in `raw_posts`, so a rerun fetches only what is missing. A rerun of a 400-post archive costs 20 API requests, about 100 s.
- A page that fails ends the read, as now. The button reruns it.
- The `wordpress` plugin has its own workers, apart from `rss` and `page`. A large archive waits on the pacer for its own host only.

### Always fetch the article page

`rss.FetchPost` fetches the article page for every item, not only excerpt items. The `excerpt` flag goes away. `wordpress` embeds `rss`, so it gets this too.

`ProcessFeed` sets `RawContent` to the page and keeps the feed's title, content, authors and date.

`Service.extract` in `extract.go` changes one rule: the page's extracted content replaces the feed's content only when it is longer in plain-text runes. Fields the feed gave still win, as now.

A failed page fetch keeps the feed's content with `RawContent` empty, where today the post is skipped. Then `Extract` runs on the feed content as it does for full-content feeds today.

### No inline DeepSeek in the fetch path

`linksFromOneListing` in `internal/adapter/extract/html/collect.go` calls DeepSeek inline, inside the `page` plugin's `FetchFeed`. It moves into the extractor's own goroutine, like `collectListing`. The listing that started it gets no links. The next rate tick fetches the listing again and uses the saved rule.

Extraction needs no rule. With none, `content()` falls back to `contentGeneric`, and the other fields fall back to meta tags.

### Discovered ingest

Drop `dailyFetchCap` and `CheckedLastDay` from `internal/core/service/source/discovered.go`. The 7-day window and the pacer stay.

## Known tradeoffs

- **One request per post.** Every adapter fetches the article page. A feed with 20 new items on one host takes about 100 s at 5 s apart. Stored posts are never fetched again.
- **Long archives.** 400 posts take about 33 minutes. 10,000 posts take about 14 hours. There is no cap, by design.
- **No record of a backfill.** Admin cannot see whether a source was backfilled. Only the `wordpress` rows in `logs` show it.
- **A crash ends a backfill.** The button reruns it.
- **A new `page` source may wait a tick or more for its first posts.** Until its link rule is saved off-peak.
- **No fairness across hosts inside a plugin.** A plugin's 5 post workers share one queue. 20 new items on one busy host hold all 5 behind its pacer for about 100 s. Two backfills at once share the `wordpress` workers the same way, so a large archive slows a second one down. The `page` plugin works this way today.
- **A backfill slows its own source's live fetches a little.** They share the host's pacer slots, so each live request waits behind the few backfill requests already queued, up to about 30 s.

## Calls made without asking

- Backfill copies feed the `wordpress` plugin directly, outside `sourceStream` and `route`.
- Activating a `proposed` source backfills it. Reactivating a `flagged` one does not.
- A failed backfill call does not fail the source form.
- Feed content wins only when it is longer than the page's extracted content.
- A failed page fetch falls back to the feed content instead of skipping the post.
- `wordpress` posts also fetch the article page, with the API content as fallback.

## Left to the builder

- Backfill channel buffer size.
- Button placement and wording, matching the Sync button.
- Tests. At least: the endpoint's status codes, a backfill copy reaching the `wordpress` plugin without a pause on its source, `rss.FetchPost` always fetching, the content choice, a failed page fetch falling back to feed content, and the discovered ingest without its cap.
- Updating `docs/kb/backfill.md` and `docs/kb/discovered-ingest.md`.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Gap between requests to a host | 5 s | `DomainPace`, shared by every adapter. |
| Posts per WordPress API page | 20 | Fits under the 5 MB body cap. |
| Workers per plugin | 5 | `workers`, carried over. |
