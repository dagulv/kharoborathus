# Backfill

**Status:** done
**Related:** [source-lifecycle](source-lifecycle.md), [architecture](architecture.md)

## Summary

A feed shows only a source's newest posts. Backfill reads a source's history once, through strategies its adapter supports. It runs when a source is created, when a proposed source is approved, and when the Backfill button is pressed.

## Current state

- [port/fetcher.go](../../internal/core/port/fetcher.go) defines `Backfiller`, which an adapter implements when it can read history.
- [rss.go](../../internal/adapter/fetcher/rss.go) tries its strategies in order. [wordpress.go](../../internal/adapter/fetcher/wordpress.go) holds the only one so far, the WordPress REST API.
- [fetch.go](../../internal/core/service/source/fetch.go) builds a backfill plugin per adapter. [sourced.go](../../internal/core/service/source/sourced.go) routes backfills to those plugins.
- [route.go](../../internal/core/service/source/route.go) serves `POST /admin/sources/{id}/backfill`.

## Design

1. The admin form calls `POST /admin/sources/{id}/backfill` after inserting a source. Approving a proposed source and the Backfill button call it too.
2. The handler sends an enabled, active source on the backfill channel without blocking. It answers `202`, or `503` when the queue is full.
3. A backfill route sends the source to its adapter's backfill plugin. An adapter without a strategy logs `no backfill strategy`.
4. The rss adapter tries its strategies in order and uses the first that supports the site. None means `no archive`, logged at info.
5. The WordPress strategy finds the REST API through the `Link` header or `<link>` tag, with the shared pacer. A failed detection, such as a closed API answering 401 or 404, counts as no API.
6. It pages through `wp/v2/posts`, 20 posts per request, newest first, and sends each post as soon as it is read. Each post takes the language of the page or feed the API was found on.
7. Each post runs through the adapter's own `Filter`, `FetchPost` and `ProcessFeed`. Stored posts are skipped, and every article page is fetched through the pacer.

Rules:

- Backfill keeps no state. A rerun starts from the first page and skips stored posts.
- Backfills never pass `sourceStream`, so they never pause a source's rate.
- Backfill plugins have their own workers. A long archive never holds up live sources.
- Posts dated more than 7 days back start no Brave search. They still join a recent search for the same event.
- API requests log as type `wordpress`.

## Schema changes

None. `backfilled_at` and `summary_pending` from the first design are gone.

## Defaults

| Parameter                      | Value | Why                                                    |
| ------------------------------ | ----- | ------------------------------------------------------ |
| Posts per API request          | 20    | Full content fits well under the 5 MB body cap.        |
| Gap between requests to a host | 5 s   | `DomainPace`, shared by live fetches and backfills.    |
| Backfill queue                 | 16    | `BackfillQueue`. A full queue answers `503`.           |

## Open questions

- Strategies for other platforms, like Ghost, Substack or RFC 5005 feed paging.
