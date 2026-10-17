# Search discovery

**Status:** designed
**Related:** [discovered-ingest](discovered-ingest.md), [trust-model](trust-model.md)

## Summary

Searches the web with the Brave Search API for other sources on events found in trusted posts, when the event's action is a gate tag: news, press releases, company pages and filings. An LLM filters results before anything is fetched.

## Current state

- [discovery.go](../../internal/service/discovery/discovery.go) asks the LLM for up to five `entity, action, query` events per post and stores them in `discover_queue`.
- `action` is free text from [discovery.md](../../internal/service/robot/prompts/discovery.md), one or two lowercase words.
- It runs on every post, not only trusted ones.
- `ListQueue` and `MarkSearched` exist in [discovery/store.go](../../internal/service/discovery/store.go) but nothing calls them.

## Design

**Trigger: entity plus a gate action**

A search runs only for events whose `action` is the key of a tag with `enabled`, `status = 'active'` and `gate = true`. Gate tags are specific topics like `acquisition` or `contract`, so a company name combined with one of them is a searchable event.

1. Event extraction keeps running on every post, since [post linking](post-linking.md) needs events from all posts.
2. The discovery prompt gets the current gate tag keys, like the tag generator gets existing keys. `action` must be one of them when one fits, otherwise its own one or two lowercase words.
3. Extracted actions are normalized like tag keys, lowercase with underscores, and remapped through `tag_aliases`.
4. A search worker runs only while the `search_discovery` switch is on, see [rollout](rollout.md). It reads `ListQueue`, limited to items that:
   - come from trusted posts,
   - have an `action` that is a gate tag key at search time, since `gate` is recomputed daily,
   - were created inside the dedupe window. Older non-matching items are left alone instead of clogging the queue.
5. Dedupe: skip the query and `MarkSearched` when the same lowercased `entity` and `action` were searched inside the dedupe window. `discover_queue` is only unique per post, so many posts about one event would each search it.
6. Call Brave Web Search through a `Searcher` interface. One call returns both web and news results.
7. An LLM sees each result's `title`, `description`, `extra_snippets`, `page_age` and hostname plus the event's `entity` and `action`. It keeps only results about that event, from any kind of source.
8. Insert kept URLs into `link_queue` with `via = 'search'` and `discover_item_id`. Brave snippets are dropped after filtering, so only URLs are stored.
9. `MarkSearched`.

Search engine URLs never enter `link_queue`. Only result URLs do.

### Brave Web Search request

```
GET https://api.search.brave.com/res/v1/web/search
x-subscription-token: <key>
```

| Param | Value |
|---|---|
| `q` | `discover_queue.query`, max 400 characters |
| `country` | `SE` |
| `search_lang` | Post language, `sv` or `en` |
| `freshness` | `pw`, last 7 days |
| `count` | 20, the max |
| `result_filter` | `web,news` |
| `extra_snippets` | `true` |

Results are read from `web.results` and `news.results` and deduplicated by URL. Fields used: `url`, `title`, `description`, `extra_snippets`, `page_age`, `meta_url.hostname`.

## Schema changes

```sql
CREATE INDEX IF NOT EXISTS discover_queue_event_idx ON discover_queue (lower(entity), lower(action), searched_at);
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Dedupe window | 7 days | Same as freshness. |
| Results per query | 20 per result type | One call, filter cuts the rest. |
| Freshness | `pw` | Same event, not old coverage. |
| Monthly budget alert | $50 | About 10k queries at $5 per 1k. |

## Open questions

- None. `country` stays `SE` until non-Swedish sources exist.
