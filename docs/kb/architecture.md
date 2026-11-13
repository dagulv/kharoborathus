# Architecture

**Status:** done
**Related:** [trust-model](trust-model.md), [tag-lifecycle](tag-lifecycle.md), [backfill](backfill.md), [discovered-ingest](discovered-ingest.md), [search-discovery](search-discovery.md)

## Summary

Kharoborathus collects business and financial news from hand-picked sources, tags it and serves it as a searchable feed. Every worker starts from [main.go](../../cmd/main.go) and runs as an event chain: each step stores its result, then hands the next step an id over a Go channel.

## Layout

| Package | Holds |
| --- | --- |
| `internal/core/domain` | Plain types: sources, posts, tags, listings. |
| `internal/core/port` | The interfaces core calls: fetchers, the extractor, the LLM, the classifier, page fetching and probing. |
| `internal/core/service` | Business logic: what to fetch when, which posts to keep, how to tag and summarize. |
| `internal/adapter` | Integrations: HTTP fetching, HTML extraction, DeepSeek, Jev, Brave and Postgres. |
| `internal/lib` | Small helpers with no business meaning: pacer, pipeline, schedule, logs. |

Rules:

- Core holds the business logic. An adapter holds how one integration works, such as feed parsing, the WordPress REST API, pagination or HTML selectors.
- Core does not import adapter packages for integrations. It calls a port, and `main.go` passes the adapter in.
- A port is as small as its caller needs. One method is common.
- Simplicity comes first. A port is added only where it makes core easier to read. Core uses the Postgres stores and the search package directly, since an interface in front of them would add code without making anything clearer.

## Workers

| Worker | Code | Job |
| --- | --- | --- |
| HTTP | [posts/route.go](../../internal/core/service/posts/route.go), [source/route.go](../../internal/core/service/source/route.go) | Serves `/posts`, and the admin's sync, backfill and listing probe. |
| Sources | [sourced.go](../../internal/core/service/source/sourced.go) | Fetches each enabled, active source at its `rate`, and runs backfills in their own lane per adapter. |
| Rule events | [ruled.go](../../internal/core/service/source/ruled.go) | Backfills a domain's page sources when its links or pages rule is saved. |
| Quality retry | [retry.go](../../internal/core/service/source/retry.go) | Every minute: checks pending raw posts the live chain left. |
| Enrich | [enrich/run.go](../../internal/core/service/enrich/run.go) | Off peak: summarizes and tags accepted raw posts into `posts`, and queues links and searches. |
| Discovered ingest | [discovered.go](../../internal/core/service/source/discovered.go) | Fetches queued links into raw posts. |
| Search | [search.go](../../internal/adapter/search/search.go) | Searches Brave for events and queues the results Jev keeps. |

## Source pipeline

1. `sourceStream` sends a source on its rate, or on a sync.
2. The source's adapter in `port.Fetcher` reads it:
   - `rss` reads the feed.
   - `page` asks the [extractor](extraction.md) for the listing's links.

   It skips stored posts, fetches every article page through the shared [pacer](../../internal/lib/pace/pace.go) and streams each post.
3. A backfill goes through `port.Backfiller` instead, with the adapter's own strategies, see [backfill](backfill.md).
4. Core gives each post its source and language.
5. [Extract](extraction.md) the content and fields.
6. Convert to markdown and compute the simhash.
7. Insert into `raw_posts` as `pending`.
8. The [quality gate](quality-gate.md) accepts or rejects it, and accepted posts go on to enrich.

## Schema changes

None.

## Defaults

| Parameter | Value | Why |
| --- | --- | --- |
| Pipeline workers per stage | 5 | `workers` in sourced.go. |
| Gap between requests to a host | 5 s | `DomainPace`, one pacer for every adapter. |

## Open questions

- None.
