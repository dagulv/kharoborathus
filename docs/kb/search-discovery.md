# Search discovery

**Status:** done
**Related:** [discovered-ingest](discovered-ingest.md), [trust-model](trust-model.md)

## Summary

Searches the web with the Brave Search API for other sources on events found in trusted posts, when the event's action is a quality tag: news, press releases, company pages and filings. An LLM filters results before anything is fetched. Both steps are links in the event chain. No ticker polls for work. See [the design](../superpowers/specs/2026-11-08-event-driven-search-design.md).

## Current state

- [discover.go](../../internal/adapter/search/discover.go): `Discover` is step 3. It runs inside the transaction that inserts a post and returns new query ids. Step 2, which calls it, does not exist yet.
- [prompts/discovery.md](../../internal/adapter/search/prompts/discovery.md): the events part of step 2's DeepSeek prompt, as `EventsSystemPrompt` and `EventsUserPrompt`.
- [search.go](../../internal/adapter/search/search.go): `RunSearch` is step 4. It reads query ids from a channel, searches through [brave.go](../../internal/adapter/search/brave.go) and filters with [result_filter.go](../../internal/adapter/search/result_filter.go).
- No catch-up job yet. Rows a step leaves unhandled stay unhandled.

## Design

**Step 3: Discover.** Inside step 2's transaction, no LLM.

1. Trusted posts, from an enabled source with `status = 'active'`, queue up to 10 outbound markdown links in `discovery_posts` with no query id.
2. Events are grounded: the entity must be in the article and in the query.
3. Actions are normalized like tag keys and mapped through `tag_aliases`. Only active, enabled `quality` tags pass.
4. `normalized_entity` is the entity as a tag key, like `volvo_cars`.
5. A query with the same `normalized_entity` and `action` created within 7 days gets the post linked to it in `discovery_search_query_posts`. That link is the `same_event` record. No new search.
6. Otherwise trusted posts insert a new query. A `query` text conflict links to the existing row and is not searched again. Untrusted posts never create queries.
7. After commit, step 2 sends new query ids with `Enqueue`. A full channel drops the id and logs it.

**Step 4: RunSearch.** One query at a time, whenever an id arrives.

1. Skip queries with `searched_at` set.
2. Leave the query untouched while 200 or more search results are pending within 7 days, or 322 searches were paid in the last day.
3. Search Brave in the language of the query's earliest linked post.
4. Save the raw results in `result.results`, even during shutdown, since Brave billed them.
5. While DeepSeek's off-peak schedule is active, filter the results. Write the kept indexes to `result.kept` and queue the kept URLs in `discovery_posts` with the query id, skipping URLs already in `posts`.

| `searched_at` | `result` | State | Paid |
|---|---|---|---|
| NULL | NULL | Not searched. | No |
| set | NULL | Rejected by Brave with 400 or 422. Never retried. | No |
| set | `results`, no `kept` | Searched, not filtered. | Yes |
| set | `results` and `kept` | Done. | Yes |

Rules:

- 429, 5xx, unreadable responses and other errors leave the query untouched.
- No results means no LLM call. `kept` is written as `[]`.
- The filter never sees result URLs and answers with result ids. The entity is passed with underscores as spaces.
- Without `BRAVE_API_KEY` the search worker does not start.

### Brave Web Search request

```
GET https://api.search.brave.com/res/v1/web/search
x-subscription-token: <key>
```

| Param            | Value                                        |
| ---------------- | -------------------------------------------- |
| `q`              | `discovery_search_queries.query`, max 400 characters |
| `country`        | `SE`                                         |
| `search_lang`    | Post language, `sv` or `en`                  |
| `freshness`      | `pw`, last 7 days                            |
| `count`          | 20, the max                                  |
| `result_filter`  | `web,news`                                   |
| `extra_snippets` | `true`                                       |

Results are read from `web.results` and `news.results` and deduplicated by URL. Fields used: `url`, `title`, `description`, `extra_snippets`, `page_age`, `meta_url.hostname`.

## Schema changes

`tag_aliases` in [001_init.sql](../../internal/migrations/001_init.sql). Only tag promotion and manual inserts write it.

## Defaults

| Parameter                    | Value              | Why                                                   |
| ---------------------------- | ------------------ | ----------------------------------------------------- |
| Dedupe window                | 7 days             | Same as freshness.                                    |
| Results per query            | 20 per result type | One call, filter cuts the rest.                       |
| Freshness                    | `pw`               | Same event, not old coverage.                         |
| Monthly budget alert         | $50                | About 10k queries at $5 per 1k.                       |
| Daily search cap             | 322                | $50 a month at $5 per 1k queries over a 31-day month. |
| Pending search results limit | 200                | One day of ingest at its daily cap.                   |
| Outbound links per post      | 10                 | Old link extraction cap.                              |
| Search queue                 | 100                | Channel buffer, `SearchQueue`.                        |

## Open questions

- None. `country` stays `SE` until non-Swedish sources exist.
