# Search discovery

**Status:** designed
**Related:** [discovered-ingest](discovered-ingest.md), [trust-model](trust-model.md)

## Summary

Searches the web for other coverage of events found in trusted posts. An LLM filters results before anything is fetched.

## Current state

- [discovery.go](../../internal/service/discovery/discovery.go) asks the LLM for up to five `entity, action, query` events per post and stores them in `discover_queue`.
- It runs on every post, not only trusted ones.
- `ListQueue` and `MarkSearched` exist in [discovery/store.go](../../internal/service/discovery/store.go) but nothing calls them.

## Design

1. Limit `ListUndiscoveredPosts` to trusted posts.
2. A search worker reads `ListQueue`.
3. Run the query against the search API behind a `Searcher` interface.
4. An LLM sees each result's title and snippet plus the event's `entity` and `action`. It keeps only results about that event.
5. Insert kept URLs into `link_queue` with `via = 'search'` and `discover_item_id`.
6. `MarkSearched`.

Search engine URLs never enter `link_queue`. Only result URLs do.

## Schema changes

None. `discover_queue` already has `searched_at`.

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Results per query | 10 | First page only. |
| Result recency | 7 days | Same event, not old coverage. |

## Open questions

- Which search API? Not chosen yet.
