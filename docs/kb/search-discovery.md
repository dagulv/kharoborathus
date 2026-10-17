# Search discovery

**Status:** done
**Related:** [discovered-ingest](discovered-ingest.md), [trust-model](trust-model.md)

## Summary

Searches the web with the Brave Search API for other sources on events found in trusted posts, when the event's action is a gate tag: news, press releases, company pages and filings. An LLM filters results before anything is fetched.

## Current state

- [discovery.go](../../internal/service/discovery/discovery.go) extracts events from every post. The prompt lists the gate tag keys, actions become tag keys and are remapped through `tag_aliases`.
- [search.go](../../internal/service/discovery/search.go) searches while `search_discovery` is on, through [brave.go](../../internal/service/discovery/brave.go), and filters results with [search_filter.go](../../internal/service/robot/search_filter.go).
- Admin shows Brave searches in the last 24 hours against the daily cap.
- `discover_queue_event_idx` serves the dedupe check and [post linking](post-linking.md).

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
5. Dedupe: skip the query and `MarkSearched` when the same lowercased `entity` and `action` had a paid search inside the dedupe window. Rows marked without a call do not count, or the window would keep sliding. `discover_queue` is only unique per post, so many posts about one event would each search it.
6. Call Brave Web Search through a `Searcher` interface. One call returns both web and news results.
7. An LLM sees each result's `title`, `description`, `extra_snippets`, `page_age` and hostname plus the event's `entity` and `action`. It keeps only results about that event, from any kind of source.
8. Insert kept URLs into `link_queue` with `via = 'search'` and `discover_item_id`. Brave snippets are dropped after filtering, so only URLs are stored.
9. `MarkSearched`.

Rules:

- At most 330 searches in a rolling 24 hours, the $50 monthly budget spread over 30 days.
- `result_count` holds the Brave result count of a paid call. It stays NULL when an item is marked searched without a call, so usage counts only paid calls.
- Newest items first. When the cap holds back a backlog, older items age out of the window.
- `search_lang` only for `sv` and `en`.
- 429, 5xx and network errors stop the tick and leave the item. 400 and 422 mark it searched without a call. Other statuses stop the tick.
- The tick follows the LLM schedule. If the filter fails after a search, the results are dropped, the item still counts as a paid search, and the worker pauses 30 minutes so an outage cannot drain the budget.
- No searches while `discovered_ingest` is off or the classifier is mocked, since nothing would fetch the results.
- No searches while 200 or more `via = 'search'` links are pending inside the window, one day of ingest at its cap.
- No results means no LLM call.
- Result URLs pass the link extraction filter, without its 10-link cap, and skip URLs already in `posts`.
- The filter never sees result URLs and answers with result ids.
- Without `BRAVE_API_KEY` the search worker does not start.

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
ALTER TABLE discover_queue ADD COLUMN result_count INT;
```

## Defaults

| Parameter | Value | Why |
|---|---|---|
| Dedupe window | 7 days | Same as freshness. |
| Results per query | 20 per result type | One call, filter cuts the rest. |
| Freshness | `pw` | Same event, not old coverage. |
| Monthly budget alert | $50 | About 10k queries at $5 per 1k. |
| Daily search cap | 330 | $50 a month at $5 per 1k queries over 30 days. |
| Searches per tick | 5 | Every minute, inside the cap. |
| Pending search results limit | 200 | One day of ingest at its daily cap. |

## Open questions

- None. `country` stays `SE` until non-Swedish sources exist.
