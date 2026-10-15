# Search discovery

**Status:** designed
**Related:** [discovered-ingest](discovered-ingest.md), [trust-model](trust-model.md)

## Summary

Searches Brave News for other coverage of events found in trusted posts. An LLM filters results before anything is fetched.

## Current state

- [discovery.go](../../internal/service/discovery/discovery.go) asks the LLM for up to five `entity, action, query` events per post and stores them in `discover_queue`.
- It runs on every post, not only trusted ones.
- `ListQueue` and `MarkSearched` exist in [discovery/store.go](../../internal/service/discovery/store.go) but nothing calls them.

## Design

1. Limit `ListUndiscoveredPosts` to trusted posts.
2. A search worker reads `ListQueue`.
3. Dedupe: skip the query and `MarkSearched` when the same lowercased `entity` and `action` were searched inside the dedupe window. `discover_queue` is only unique per post, so many posts about one event would each search it.
4. Call Brave News through a `Searcher` interface.
5. An LLM sees each result's `title`, `description`, `extra_snippets` and `page_age` plus the event's `entity` and `action`. It keeps only results about that event.
6. Insert kept URLs into `link_queue` with `via = 'search'` and `discover_item_id`.
7. `MarkSearched`.

Search engine URLs never enter `link_queue`. Only result URLs do.

### Brave News request

```
GET https://api.search.brave.com/res/v1/news/search
```

| Param | Value |
|---|---|
| `q` | `discover_queue.query` |
| `country` | `SE` |
| `search_lang` | Post language, `sv` or `en` |
| `freshness` | `pw`, last 7 days |
| `count` | 20, max 50 |
| `extra_snippets` | `true` |

Result fields used: `url`, `title`, `description`, `extra_snippets`, `page_age`, `meta_url.hostname`.

Auth header and exact path are not verified yet. Check the API reference before implementing.

## Schema changes

```sql
CREATE INDEX IF NOT EXISTS discover_queue_event_idx ON discover_queue (lower(entity), lower(action), searched_at);
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Dedupe window | 7 days | Same as freshness. |
| Results per query | 20 | One call, filter cuts the rest. |
| Freshness | `pw` | Same event, not old coverage. |
| Monthly budget alert | $50 | About 10k queries at $5 per 1k. |

## Open questions

- Does the Search plan allow storing result URLs in `link_queue`, or is a storage-rights plan needed?
- Should `country` follow the source's country once non-Swedish sources are added?
